# Run through `brew ruby`; load the real Homebrew DSL without installing the App.
require "cask/cask_loader"
require "cask/installer"

ARGV.each do |path|
  cask = Cask::CaskLoader::FromContentLoader.new(File.read(path)).load(config: nil)
  raise "Unexpected cask token" unless cask.token == "pot"

  if cask.disabled?
    raise "Disabled cask must not invent archive metadata" if cask.version || cask.url || cask.sha256

    begin
      Cask::Installer.new(cask).check_deprecate_disable
      raise "Disabled cask was installable"
    rescue Cask::CaskCannotBeInstalledError => e
      raise unless e.message.include?("first macOS release")
    end
  else
    raise "Missing fixed version" unless cask.version.to_s.match?(/\A\d+\.\d+\.\d+\z/)
    raise "Missing archive digest" unless cask.sha256.to_s.match?(/\A[a-f0-9]{64}\z/)
    expected = "https://github.com/jjyr/pot/releases/download/v#{cask.version}/Pot-#{cask.version}-arm64.zip"
    raise "Unexpected download URL" unless cask.url.to_s == expected
  end
  puts "Homebrew loaded #{path}: #{cask.disabled? ? 'disabled before publication' : cask.version}"
end
