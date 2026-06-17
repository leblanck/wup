package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// Configuration (links, weather, refresh intervals) is loaded at startup from
// a JSON file — see config.go. The link types and the runtime config vars
// (links, weatherLocation, weatherUnits, weatherEvery, hnEvery, hnCount) live
// there too.

// ============================================================================
// PALETTE (Gruvbox-ish) + styles
// ============================================================================

const (
	colBg     = "#282828"
	colFg     = "#ebdbb2"
	colGray   = "#928374"
	colAqua   = "#8ec07c"
	colYellow = "#fabd2f"
	colOrange = "#fe8019"
	colRed    = "#fb4934"
	colBlue   = "#83a598"
)

var (
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colYellow)).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colGray))
	greetStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colAqua)).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colRed))
	tickerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue)).Bold(true)
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colBg)).Background(lipgloss.Color(colAqua))
	markStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(colYellow)).Bold(true)
)

// ASCII wordmark shown in the header (ANSI Regular style). Each line is the
// same width so it joins cleanly next to the greeting block.
var wordmarkWUP = []string{
	"██     ██ ██    ██ ██████ ",
	"██     ██ ██    ██ ██   ██",
	"██  █  ██ ██    ██ ██████ ",
	"██ ███ ██ ██    ██ ██     ",
	" ███ ███   ██████  ██     ",
}

// ============================================================================
// MESSAGES
// ============================================================================

type tickMsg time.Time
type weatherMsg struct {
	text string
	err  error
}
type hnMsg struct {
	stories []hnStory
	err     error
}
type weatherTickMsg struct{}
type hnTickMsg struct{}
type statusMsg struct{ text string }

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ============================================================================
// SYSTEM METRICS
// ============================================================================

type stats struct {
	cpuPercent  float64
	memUsed     uint64
	memTotal    uint64
	memPercent  float64
	diskUsed    uint64
	diskTotal   uint64
	diskPercent float64
	hostname    string
	platform    string
	uptime      uint64
}

func diskPath() string {
	if runtime.GOOS == "windows" {
		return "C:\\"
	}
	return "/"
}

func collect() stats {
	var s stats
	if p, err := cpu.Percent(0, false); err == nil && len(p) > 0 {
		s.cpuPercent = p[0]
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		s.memUsed, s.memTotal, s.memPercent = vm.Used, vm.Total, vm.UsedPercent
	}
	if du, err := disk.Usage(diskPath()); err == nil {
		s.diskUsed, s.diskTotal, s.diskPercent = du.Used, du.Total, du.UsedPercent
	}
	if hi, err := host.Info(); err == nil {
		s.hostname = hi.Hostname
		s.platform = strings.TrimSpace(fmt.Sprintf("%s %s", hi.Platform, hi.PlatformVersion))
		s.uptime = hi.Uptime
	}
	return s
}

// ============================================================================
// NETWORK: weather + hacker news
// ============================================================================

var httpClient = &http.Client{Timeout: 12 * time.Second}

func fetchWeather() tea.Cmd {
	return func() tea.Msg {
		loc := strings.ReplaceAll(weatherLocation, " ", "+")
		// %-directives must stay raw; spaces encoded as '+'.
		format := "%l:+%c+%t+(feels+%f)++%w++hum+%h"
		u := "https://wttr.in/" + loc + "?format=" + format
		if weatherUnits != "" {
			u += "&" + weatherUnits
		}
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return weatherMsg{err: err}
		}
		// wttr.in returns plain text for curl-like clients.
		req.Header.Set("User-Agent", "curl/8.4.0")
		resp, err := httpClient.Do(req)
		if err != nil {
			return weatherMsg{err: err}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return weatherMsg{err: fmt.Errorf("wttr.in status %d", resp.StatusCode)}
		}
		return weatherMsg{text: strings.TrimSpace(string(body))}
	}
}

type hnStory struct {
	id       int
	title    string
	url      string
	score    int
	by       string
	comments int
}

func getJSON(u string, v interface{}) error {
	resp, err := httpClient.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func fetchHN() tea.Cmd {
	return func() tea.Msg {
		var ids []int
		if err := getJSON("https://hacker-news.firebaseio.com/v0/topstories.json", &ids); err != nil {
			return hnMsg{err: err}
		}
		if len(ids) > hnCount {
			ids = ids[:hnCount]
		}
		stories := make([]hnStory, len(ids))
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func(i, id int) {
				defer wg.Done()
				var raw struct {
					Title       string `json:"title"`
					URL         string `json:"url"`
					Score       int    `json:"score"`
					By          string `json:"by"`
					Descendants int    `json:"descendants"`
				}
				if err := getJSON(fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id), &raw); err != nil {
					stories[i] = hnStory{id: id, title: "(failed to load)"}
					return
				}
				stories[i] = hnStory{
					id: id, title: raw.Title, url: raw.URL,
					score: raw.Score, by: raw.By, comments: raw.Descendants,
				}
			}(i, id)
		}
		wg.Wait()
		return hnMsg{stories: stories}
	}
}

// ============================================================================
// OPENING LINKS
// ============================================================================

func opener(target string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		return exec.Command("xdg-open", target)
	}
}

func openLinkCmd(l link) tea.Cmd {
	return func() tea.Msg {
		var c *exec.Cmd
		switch l.kind {
		case kindApp:
			if runtime.GOOS == "darwin" {
				c = exec.Command("open", "-a", l.target)
			} else {
				c = opener(l.target)
			}
		case kindCmd:
			c = exec.Command("sh", "-c", l.target)
		default:
			c = opener(l.target)
		}
		if err := c.Start(); err != nil {
			return statusMsg{fmt.Sprintf("✗ couldn't open %s: %v", l.name, err)}
		}
		return statusMsg{"→ opened " + l.name}
	}
}

func openStoryCmd(s hnStory) tea.Cmd {
	target := s.url
	if target == "" {
		target = fmt.Sprintf("https://news.ycombinator.com/item?id=%d", s.id)
	}
	return func() tea.Msg {
		if err := opener(target).Start(); err != nil {
			return statusMsg{fmt.Sprintf("✗ couldn't open story: %v", err)}
		}
		return statusMsg{"→ opened story in browser"}
	}
}

// ============================================================================
// MODEL
// ============================================================================

type focusArea int

const (
	focusLinks focusArea = iota
	focusHN
)

type model struct {
	width, height int
	ready         bool

	st      stats
	history []float64

	weather    string
	weatherErr error
	weatherAt  time.Time

	stories []hnStory
	hnErr   error
	hnAt    time.Time

	focus        focusArea
	linkSel      int
	hnSel        int
	tickerOffset int
	status       string
}

func newModel() model {
	return model{history: make([]float64, 0, 256)}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tickMsg(time.Now()) },
		fetchWeather(),
		fetchHN(),
		tea.Tick(weatherEvery, func(time.Time) tea.Msg { return weatherTickMsg{} }),
		tea.Tick(hnEvery, func(time.Time) tea.Msg { return hnTickMsg{} }),
	)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "tab":
			if m.focus == focusLinks {
				m.focus = focusHN
			} else {
				m.focus = focusLinks
			}
			return m, nil
		case "up", "k":
			if m.focus == focusLinks {
				m.linkSel = clampInt(m.linkSel-1, 0, len(links)-1)
			} else {
				m.hnSel = clampInt(m.hnSel-1, 0, maxInt(len(m.stories)-1, 0))
			}
			return m, nil
		case "down", "j":
			if m.focus == focusLinks {
				m.linkSel = clampInt(m.linkSel+1, 0, len(links)-1)
			} else {
				m.hnSel = clampInt(m.hnSel+1, 0, maxInt(len(m.stories)-1, 0))
			}
			return m, nil
		case "enter":
			if m.focus == focusLinks && len(links) > 0 {
				return m, openLinkCmd(links[m.linkSel])
			}
			if m.focus == focusHN && m.hnSel < len(m.stories) {
				return m, openStoryCmd(m.stories[m.hnSel])
			}
			return m, nil
		case "r":
			m.status = "refreshing…"
			return m, tea.Batch(fetchWeather(), fetchHN())
		default:
			// number shortcuts 1-9 open the corresponding link
			if len(msg.String()) == 1 {
				c := msg.String()[0]
				if c >= '1' && c <= '9' {
					idx := int(c - '1')
					if idx < len(links) {
						return m, openLinkCmd(links[idx])
					}
				}
			}
			return m, nil
		}

	case tickMsg:
		m.st = collect()
		m.history = append(m.history, m.st.cpuPercent)
		if len(m.history) > 256 {
			m.history = m.history[len(m.history)-256:]
		}
		m.tickerOffset++
		return m, tick()

	case weatherMsg:
		m.weatherAt = time.Now()
		if msg.err != nil {
			m.weatherErr = msg.err
		} else {
			m.weatherErr = nil
			m.weather = msg.text
		}
		return m, nil

	case weatherTickMsg:
		return m, tea.Batch(fetchWeather(),
			tea.Tick(weatherEvery, func(time.Time) tea.Msg { return weatherTickMsg{} }))

	case hnMsg:
		m.hnAt = time.Now()
		if msg.err != nil {
			m.hnErr = msg.err
		} else {
			m.hnErr = nil
			m.stories = msg.stories
			m.hnSel = clampInt(m.hnSel, 0, maxInt(len(m.stories)-1, 0))
		}
		return m, nil

	case hnTickMsg:
		return m, tea.Batch(fetchHN(),
			tea.Tick(hnEvery, func(time.Time) tea.Msg { return hnTickMsg{} }))

	case statusMsg:
		m.status = msg.text
		return m, nil
	}
	return m, nil
}

// ============================================================================
// SMALL HELPERS
// ============================================================================

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func runeLen(s string) int { return len([]rune(s)) }

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

func padRight(s string, w int) string {
	n := w - runeLen(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

func wrap(s string, w int) string {
	if w <= 0 {
		return s
	}
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, word := range words {
		if cur == "" {
			cur = word
		} else if runeLen(cur)+1+runeLen(word) <= w {
			cur += " " + word
		} else {
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func fmtUptime(secs uint64) string {
	d := secs / 86400
	h := (secs % 86400) / 3600
	mn := (secs % 3600) / 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh %dm", d, h, mn)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, mn)
	default:
		return fmt.Sprintf("%dm", mn)
	}
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

func colorFor(p float64) string {
	switch {
	case p >= 85:
		return colRed
	case p >= 60:
		return colOrange
	case p >= 35:
		return colYellow
	default:
		return colAqua
	}
}

var blocks = []rune("▁▂▃▄▅▆▇█")

func sparkline(vals []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	var b strings.Builder
	for i := 0; i < width-len(vals); i++ {
		b.WriteRune(' ')
	}
	for _, v := range vals {
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		idx := int(v / 100 * float64(len(blocks)-1))
		if idx > len(blocks)-1 {
			idx = len(blocks) - 1
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(colorFor(v))).Render(string(blocks[idx])))
	}
	return b.String()
}

func miniBar(p float64, width int) string {
	if width < 1 {
		width = 1
	}
	filled := int(p / 100 * float64(width))
	filled = clampInt(filled, 0, width)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colorFor(p))).Render(bar)
}

func domainOf(raw string) string {
	if raw == "" {
		return "news.ycombinator.com"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(u.Host, "www.")
}

// ============================================================================
// PANEL builders (return body text for a given inner width)
// ============================================================================

func panel(width int, focused bool, title, body string) string {
	bc := colGray
	if focused {
		bc = colAqua
	}
	inner := width - 4
	if inner < 8 {
		inner = 8
	}
	st := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(bc)).
		Padding(0, 1).
		Width(inner)
	return st.Render(titleStyle.Render(title) + "\n" + body)
}

func (m model) weatherBody(inner int) string {
	var s string
	switch {
	case m.weatherErr != nil:
		s = errStyle.Render(truncate("unavailable: "+m.weatherErr.Error(), inner))
	case m.weather == "":
		s = dimStyle.Render("loading…")
	default:
		s = wrap(m.weather, inner)
	}
	return s + "\n" + dimStyle.Render("updated "+ago(m.weatherAt))
}

func (m model) metricsBody(inner int) string {
	barW := clampInt(inner-13, 4, 40)
	line := func(label string, pct float64) string {
		return fmt.Sprintf("%-4s %s %s", label, miniBar(pct, barW),
			lipgloss.NewStyle().Foreground(lipgloss.Color(colorFor(pct))).Render(fmt.Sprintf("%4.0f%%", pct)))
	}
	lines := []string{
		line("CPU", m.st.cpuPercent),
		"     " + sparkline(m.history, barW),
		line("MEM", m.st.memPercent),
		dimStyle.Render(fmt.Sprintf("     %s / %s", humanBytes(m.st.memUsed), humanBytes(m.st.memTotal))),
		line("DSK", m.st.diskPercent),
		dimStyle.Render(fmt.Sprintf("     %s / %s", humanBytes(m.st.diskUsed), humanBytes(m.st.diskTotal))),
	}
	return strings.Join(lines, "\n")
}

func (m model) linksBody(inner int, focused bool) string {
	var b strings.Builder
	for i, l := range links {
		marker := " "
		if i < 9 {
			marker = fmt.Sprintf("%d", i+1)
		}
		text := fmt.Sprintf("[%s] %s", marker, l.name)
		prefix := "  "
		selected := focused && i == m.linkSel
		if selected {
			prefix = "▸ "
		}
		full := truncate(prefix+text, inner)
		if selected {
			full = selStyle.Render(padRight(full, inner))
		}
		b.WriteString(full)
		if i < len(links)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m model) hnBody(inner int, focused bool) string {
	if m.hnErr != nil {
		return errStyle.Render(truncate("unavailable: "+m.hnErr.Error(), inner))
	}
	if len(m.stories) == 0 {
		return dimStyle.Render("loading…")
	}
	var b strings.Builder
	for i, s := range m.stories {
		title := s.title
		if title == "" {
			title = "(loading)"
		}
		head := truncate(fmt.Sprintf("%2d. %s", i+1, title), inner)
		if focused && i == m.hnSel {
			head = selStyle.Render(padRight(head, inner))
		}
		meta := truncate(fmt.Sprintf("    %d▲  %d comments  %s", s.score, s.comments, domainOf(s.url)), inner)
		b.WriteString(head + "\n" + dimStyle.Render(meta))
		if i < len(m.stories)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m model) tickerLine(width int) string {
	if width <= 0 {
		return ""
	}
	if len(m.stories) == 0 {
		return dimStyle.Render(strings.Repeat("·", width))
	}
	parts := make([]string, 0, len(m.stories))
	for i, s := range m.stories {
		if s.title == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d. %s (%d▲)", i+1, s.title, s.score))
	}
	full := strings.Join(parts, "    •    ") + "    •    "
	r := []rune(full)
	if len(r) == 0 {
		return ""
	}
	off := m.tickerOffset % len(r)
	out := make([]rune, width)
	for i := 0; i < width; i++ {
		out[i] = r[(off+i)%len(r)]
	}
	return tickerStyle.Render(string(out))
}

// ============================================================================
// VIEW
// ============================================================================

func (m model) View() string {
	if !m.ready || m.width == 0 {
		return "Loading wup…"
	}

	totalW := m.width
	if totalW > 160 {
		totalW = 160
	}
	if totalW < 30 {
		totalW = 30
	}

	now := time.Now()
	greet := "Good evening"
	switch h := now.Hour(); {
	case h < 12:
		greet = "Good morning"
	case h < 18:
		greet = "Good afternoon"
	}

	info := lipgloss.JoinVertical(lipgloss.Left,
		greetStyle.Render(greet),
		dimStyle.Render(now.Format("Mon Jan 2 · 3:04 PM")),
		dimStyle.Render(fmt.Sprintf("%s · up %s", ifEmpty(m.st.hostname, "host"), fmtUptime(m.st.uptime))),
	)

	var header string
	if totalW >= 50 {
		mark := markStyle.Render(strings.Join(wordmarkWUP, "\n"))
		header = lipgloss.JoinHorizontal(lipgloss.Center, mark, "   ", info)
	} else {
		// too narrow for the wordmark — fall back to a compact one-liner
		header = greetStyle.Render(greet) + dimStyle.Render(fmt.Sprintf("  ·  %s · up %s",
			now.Format("Mon Jan 2 · 3:04 PM"), fmtUptime(m.st.uptime)))
	}

	ticker := m.tickerLine(totalW)

	twoCol := totalW >= 88
	var body string
	if twoCol {
		leftW := 36
		rightW := totalW - leftW - 1
		left := lipgloss.JoinVertical(lipgloss.Left,
			panel(leftW, false, " Weather", m.weatherBody(leftW-4)),
			panel(leftW, false, " System", m.metricsBody(leftW-4)),
			panel(leftW, m.focus == focusLinks, " Launch", m.linksBody(leftW-4, m.focus == focusLinks)),
		)
		right := panel(rightW, m.focus == focusHN, " Hacker News — Top 10", m.hnBody(rightW-4, m.focus == focusHN))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		body = lipgloss.JoinVertical(lipgloss.Left,
			panel(totalW, false, " Weather", m.weatherBody(totalW-4)),
			panel(totalW, false, " System", m.metricsBody(totalW-4)),
			panel(totalW, m.focus == focusLinks, " Launch", m.linksBody(totalW-4, m.focus == focusLinks)),
			panel(totalW, m.focus == focusHN, " Hacker News — Top 10", m.hnBody(totalW-4, m.focus == focusHN)),
		)
	}

	help := dimStyle.Render("tab focus · ↑↓ move · enter open · 1-9 launch · r refresh · q quit")
	meta := dimStyle.Render(fmt.Sprintf("wx %s · hn %s", ago(m.weatherAt), ago(m.hnAt)))
	footer := help + "    " + meta
	if m.status != "" {
		footer = lipgloss.NewStyle().Foreground(lipgloss.Color(colAqua)).Render(m.status) + "\n" + footer
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, ticker, body, footer)
}

func ifEmpty(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func main() {
	configFlag := flag.String("config", "", "path to config.json (default: $XDG_CONFIG_HOME/wup/config.json)")
	flag.Parse()

	if err := loadConfig(*configFlag); err != nil {
		fmt.Fprintln(os.Stderr, "wup: config error:", err)
		os.Exit(1)
	}

	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("wup error:", err)
		os.Exit(1)
	}
}
