# Load the real Homebrew Formula DSL without installing into the user's Cellar.
# Run with: brew ruby scripts/tests/homebrew-formula.rb Formula/pot-cli.rb dist/pot-cli.rb
require "formula"
require "formulary"
require "tmpdir"
require "digest"
require "English"

ARGV.each do |path|
  formula = Formulary.factory(Pathname.new(File.expand_path(path)))
  raise "Unexpected formula name" unless formula.name == "pot-cli"
  raise "Missing fixed version" unless formula.version.to_s.match?(/\A\d+\.\d+\.\d+\z/)
  raise "Missing archive digest" unless formula.stable.checksum.to_s.match?(/\A[a-f0-9]{64}\z/)

  expected = "https://github.com/jjyr/pot/releases/download/v#{formula.version}/pot-#{formula.version}-darwin-arm64.tar.gz"
  raise "Unexpected CLI URL" unless formula.stable.url == expected
  raise "Missing installation test" unless formula.test_defined?

  puts "Homebrew loaded #{path}: #{formula.version}"

  # Optional native installation fixture. All writes stay in this temporary
  # prefix; the user's Cellar, installed App and data roots are never opened.
  next unless (archive = ENV["POT_CLI_TEST_ARCHIVE"])
  raise "Fixture archive checksum mismatch" unless Digest::SHA256.file(archive).hexdigest == formula.stable.checksum.to_s

  Dir.mktmpdir("pot formula test ") do |temporary|
    root = Pathname.new(temporary)
    system "tar", "-xzf", archive, "-C", temporary, exception: true
    formula.define_singleton_method(:prefix) { |*| root/"prefix" }
    Dir.chdir(root/"pot-darwin-arm64") { formula.install }
    launcher = root/"linked bin/pot-cli"
    launcher.dirname.mkpath
    launcher.make_symlink(formula.bin/"pot-cli")
    output = IO.popen([launcher.to_s, "--version"], &:read)
    raise "Installed CLI did not run" unless $CHILD_STATUS.success? && output.include?(formula.version.to_s)
    raise "CLI is still registered for internal updates" if (formula.libexec/"Resources/update.json").exist?
    raise "Lost bundled Skills" unless (formula.libexec/"Resources/skills").directory?

    [formula.libexec/"bin/pot", formula.libexec/"Resources/node/bin/node"].each do |binary|
      system "codesign", "--verify", "--strict", binary.to_s, exception: true
    end
    system (formula.libexec/"Resources/node/bin/node").to_s, "--version", exception: true
    puts "Installed isolated Formula, ran pot-cli through a symlink, and verified executable signatures"
  end
end
