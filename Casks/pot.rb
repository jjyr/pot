cask "pot" do
  version "0.0.1"
  sha256 "72a1d84536d52d08653c7270483027041737df805f40e9a3536d30dcfba24b9b"

  url "https://github.com/jjyr/pot/releases/download/v#{version}/Pot-#{version}-arm64.zip"
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  auto_updates true

  app "Pot.app"

end
