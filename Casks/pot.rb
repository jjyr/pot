cask "pot" do
  version "0.0.4"
  sha256 "fd5b4fe06c1646f57a676c59a303b85263bdcbaedf6e53014c9620041da13b90"

  url "https://github.com/jjyr/pot/releases/download/v#{version}/Pot-#{version}-arm64.zip"
  name "Pot"
  desc "Personal AI assistant with local and explicit remote task execution"
  homepage "https://github.com/jjyr/pot"

  depends_on arch: :arm64
  depends_on macos: :tahoe

  auto_updates true

  app "Pot.app"

end
