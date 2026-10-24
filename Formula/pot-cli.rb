class PotCli < Formula
  desc "Pot headless CLI, Runtime and Web UI"
  homepage "https://github.com/jjyr/pot"
  url "https://github.com/jjyr/pot/releases/download/v0.0.1/pot-0.0.1-darwin-arm64.tar.gz"
  version "0.0.1"
  sha256 "ba5afe3a224f49ff96370559146ad8a92042c03cc0e5904101240d5be7a4d282"
  license "MIT"

  depends_on macos: :tahoe
  depends_on arch: :arm64

  def install
    libexec.install "bin", "Resources"
    # Homebrew owns this installation. Preserve the signed executables, but
    # do not register a host that could replace the Cellar installation.
    (libexec/"Resources/update.json").unlink
    # Exec the real binary so even the published CLI's unresolved macOS
    # os.Executable path locates ../Resources inside libexec.
    (bin/"pot-cli").write <<~SH
      #!/bin/sh
      exec "#{libexec}/bin/pot" "$@"
    SH
    (bin/"pot-cli").chmod 0755
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/pot-cli --version")
    assert_path_exists libexec/"Resources/node/bin/node"
    assert_path_exists libexec/"Resources/skills"
    refute_path_exists libexec/"Resources/update.json"
  end
end
