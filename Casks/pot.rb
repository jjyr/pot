cask "pot" do
  version "0.0.2"
  sha256 "b2aaa384f82f04f97686420ac17128610f52d38b1059eaeca23d751f2136c274"

  url "https://github.com/jjyr/pot/releases/download/v#{version}/Pot-#{version}-arm64.zip"
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  auto_updates true

  app "Pot.app"

end
