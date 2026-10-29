cask "pot" do
  version "0.0.8"
  sha256 "e615926838c98e1d670402af8fdcdfd2d55efdd22aa343fa66260dec29053a83"

  url "https://github.com/jjyr/pot/releases/download/v#{version}/Pot-#{version}-arm64.zip"
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  auto_updates true

  app "Pot.app"

end
