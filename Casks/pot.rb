cask "pot" do
  version "0.0.5"
  sha256 "072ae6742d4e41206ce983d254b9c519b6d0d5e645a7f4e1c2263376d90cb0fe"

  url "https://github.com/jjyr/pot/releases/download/v#{version}/Pot-#{version}-arm64.zip"
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  auto_updates true

  app "Pot.app"

end
