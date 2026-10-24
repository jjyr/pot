cask "pot" do
  version "0.0.1"
  sha256 "a049a4d0446e523046e9a78d26258db3851743f3925850bdd93d2d92f372dd78"

  url "https://github.com/jjyr/pot/releases/download/v#{version}/Pot-#{version}-arm64.zip"
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  auto_updates true

  app "Pot.app"

end
