class Wup < Formula
  desc "Terminal homepage dashboard: metrics, links, weather, Hacker News"
  homepage "https://github.com/YOURUSER/wup"
  version "0.1.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/YOURUSER/wup/releases/download/v0.1.0/wup_0.1.0_darwin_arm64.tar.gz"
      sha256 "PASTE_ARM64_SHA256_HERE"
    end
    on_intel do
      url "https://github.com/YOURUSER/wup/releases/download/v0.1.0/wup_0.1.0_darwin_amd64.tar.gz"
      sha256 "PASTE_AMD64_SHA256_HERE"
    end
  end

  def install
    bin.install "wup"
  end

  test do
    assert_predicate bin/"wup", :exist?
  end
end