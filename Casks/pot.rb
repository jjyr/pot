# No macOS release has been published. The release sync replaces this entire
# definition with packaging/homebrew/pot.rb.in and the verified final ZIP digest.
cask "pot" do
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  disable! date: "2026-01-01", because: "the first macOS release and its reviewed cask update are pending"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  app "Pot.app"
end
