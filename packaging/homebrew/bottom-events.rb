class BottomEvents < Formula
  desc "Read-only process start and stop logger"
  homepage "https://github.com/donomii/bottom"
  version "0.1.2"
  license "GPL-3.0-only"

  on_macos do
    on_arm do
      url "https://github.com/donomii/bottom/releases/download/v0.1.2/bottom-events_0.1.2_darwin_arm64.tar.gz"
      sha256 "6d429c61c626f34cbf015fb9c68e62ef220414345dd8b419edc832d5606fa773"
    end
    on_intel do
      url "https://github.com/donomii/bottom/releases/download/v0.1.2/bottom-events_0.1.2_darwin_amd64.tar.gz"
      sha256 "369aeddf7c748ff62420aac6d08a04abab04ab50339a7516de5de928a488b032"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/donomii/bottom/releases/download/v0.1.2/bottom-events_0.1.2_linux_arm64.tar.gz"
      sha256 "dfef5329c7f3f150e8fac87d145a2fa192a30565a0f6ae510548c972ca80ce10"
    end
    on_intel do
      url "https://github.com/donomii/bottom/releases/download/v0.1.2/bottom-events_0.1.2_linux_amd64.tar.gz"
      sha256 "153c68774c3cb459d8c7c0b4842b66e007a537a6d7ac680484068285351f2ee4"
    end
  end

  def install
    bin.install "bottom"
    man1.install "docs/bottom.1"
  end

  def caveats
    <<~EOS
      The macOS Endpoint Security backend requires an Apple-granted entitlement,
      an entitled signature, Full Disk Access, and the required privilege. Without
      them, automatic capture reports the missing requirement and uses native polling.
      See https://github.com/donomii/bottom/blob/master/docs/endpoint-security.md
    EOS
  end

  test do
    assert_match(/bottom v?0\.1\.2/, shell_output("#{bin}/bottom version"))
  end
end
