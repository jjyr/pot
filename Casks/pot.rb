cask "pot" do
  version "0.0.9"
  sha256 "06edb07c923b40e262c483ead7579834dea24d363ab95ee8444e2b773e048cbe"

  url "https://github.com/jjyr/pot/releases/download/v#{version}/Pot-#{version}-arm64.zip"
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  auto_updates true

  app "Pot.app"

end
