# wup

A "wake-up" terminal homepage: live system metrics, quick-launch links,
local weather (wttr.in), and a Hacker News top-10 watchlist with a scrolling
headline ticker. Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Lip Gloss](https://github.com/charmbracelet/lipgloss), and
[gopsutil](https://github.com/shirou/gopsutil).

## Install

Once you've published a release to your tap (see `packaging/`), install on any
machine with Homebrew. wup ships as a Homebrew **cask** (a prebuilt binary):

```sh
brew install --cask leblanck/tap/wup
```

Or tap once, then manage it by short name:

```sh
brew tap leblanck/tap
brew install --cask wup
brew upgrade --cask wup     # update to the latest release
brew uninstall --cask wup   # remove
```

## Build from source

From the `wup/` directory:

```sh
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/shirou/gopsutil/v3@latest
go mod tidy
go run .
```

Build a binary if you'd rather launch it from your shell profile:

```sh
go build -o wup .
./wup
```

## Controls

| Key        | Action                                    |
|------------|-------------------------------------------|
| `tab`      | switch focus between Launch and Hacker News |
| `↑`/`↓` or `k`/`j` | move selection in the focused panel |
| `enter`    | open the selected link / story            |
| `1`–`9`    | quick-launch the corresponding link       |
| `r`        | force-refresh weather + Hacker News        |
| `q` / `esc` / `ctrl-c` | quit                          |

Metrics refresh every second, weather every 15 minutes, HN every 5 minutes.

## Configuration

Settings live in a JSON file — no rebuilding required. On first run, wup
writes a starter config and tells you where:

```
~/.config/wup/config.json
```

(Honors `$XDG_CONFIG_HOME`. Override the path with `wup --config /some/path.json`
or the `WUP_CONFIG` env var.) Edit the file and restart wup to apply.

```json
{
  "weather": {
    "location": "",
    "units": "u",
    "refresh_minutes": 15
  },
  "hackernews": {
    "refresh_minutes": 5,
    "count": 10
  },
  "window": {
    "force_size": true,
    "cols": 125,
    "rows": 35
  },
  "links": [
    { "name": "Gmail",   "kind": "url", "target": "https://mail.google.com" },
    { "name": "VS Code", "kind": "app", "target": "Visual Studio Code" },
    { "name": "New tab", "kind": "cmd", "target": "open -a Ghostty ~" }
  ]
}
```

- **weather.location** — empty auto-detects by IP; or set e.g. `"Portland,Maine"`.
- **weather.units** — `"u"` imperial, `"m"` metric, `""` for the wttr default.
- **\*.refresh_minutes** — how often weather / HN refresh.
- **hackernews.count** — how many top stories to show (1–30).
- **window.force_size** — on launch, ask the terminal to resize to `cols`×`rows`
  via the `CSI 8 t` escape sequence. One-shot: you can resize freely afterward.
  Best-effort — many terminals ignore it, and it does nothing inside tmux. Set
  `false` to disable.
- **links** — your launcher. `kind` is one of:
  - `"url"` — any web URL (opens in your default browser)
  - `"app"` — a macOS app name, e.g. `"Visual Studio Code"` (`open -a`)
  - `"cmd"` — an arbitrary shell command, e.g. `"open -a Ghostty ~"`
  - The first 9 links get number-key shortcuts.

A malformed config is reported as an error on startup rather than silently
ignored. Anything missing or out of range falls back to a sane default.

## Notes

- If `window.force_size` doesn't take effect (Ghostty and several other
  terminals don't honor the resize sequence, and tmux blocks it), launch wup in
  a correctly-sized Ghostty window instead. Add a shell function to your rc:

  ```sh
  wup-win() {
    open -na Ghostty --args \
      --window-width=125 --window-height=35 --window-save-state=never \
      -e wup
  }
  ```

  Ghostty's window size is measured in grid cells, and on macOS it only applies
  when window-save-state is off — hence `--window-save-state=never`.
- Weather uses wttr.in with a curl-style User-Agent so it returns plain text.
- HN stories are fetched concurrently from the Firebase API, so the panel
  populates in one round-trip's worth of time rather than ten.
- The layout switches to two columns at ≥88 cols wide and stacks below that.