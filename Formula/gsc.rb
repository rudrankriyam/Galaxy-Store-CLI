class Gsc < Formula
  desc "Unofficial, automation-first CLI for the Galaxy Store Developer API"
  homepage "https://github.com/rudrankriyam/Galaxy-Store-CLI"
  license "MIT"
  head "https://github.com/rudrankriyam/Galaxy-Store-CLI.git", branch: "main"

  depends_on "go" => :build

  conflicts_with "gambit-scheme", because: "both install a `gsc` binary"
  conflicts_with "ghostscript", because: "both install a `gsc` binary"
  conflicts_with "gerbil-scheme", because: "both install a `gsc` binary"

  def install
    ldflags = [
      "-X main.version=development",
      "-X main.commit=homebrew-head",
      "-X main.date=unknown",
    ]
    system "go", "build", *std_go_args(ldflags: ldflags), "."
  end

  test do
    assert_match "development", shell_output("#{bin}/gsc version")
    assert_match "operationCount", shell_output("#{bin}/gsc capabilities --output json")
  end
end
