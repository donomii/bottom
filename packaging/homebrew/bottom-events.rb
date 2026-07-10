class BottomEvents < Formula
  desc "Read-only process lifecycle flight recorder"
  homepage "https://github.com/donomii/bottom"
  version "0.1.1"
  license "GPL-3.0-only"

  on_macos do
    on_arm do
      url "https://github.com/donomii/bottom/releases/download/v0.1.1/bottom-events_0.1.1_darwin_arm64.tar.gz"
      sha256 "9b98523a26ff80d519483835029e03fd923fba39ea5ea256c3e8c136e14d3c59"
    end
    on_intel do
      url "https://github.com/donomii/bottom/releases/download/v0.1.1/bottom-events_0.1.1_darwin_amd64.tar.gz"
      sha256 "7ad73ebe403e99dd3ae74b0a26006c35f2a36abfaf1d36c447fb557fa765ff6d"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/donomii/bottom/releases/download/v0.1.1/bottom-events_0.1.1_linux_arm64.tar.gz"
      sha256 "fb16b22525ebccdd455fed1acfd2a31987215f9915a218d1ca947f0a430d394d"
    end
    on_intel do
      url "https://github.com/donomii/bottom/releases/download/v0.1.1/bottom-events_0.1.1_linux_amd64.tar.gz"
      sha256 "ab567e8abbb3c47823ad1d00624de00217b4b0877a4a988f1bce34e31de443e8"
    end
  end

  def install
    bin.install "bottom"
    man1.install "docs/bottom.1"
  end

  test do
    assert_match "bottom 0.1.1", shell_output("#{bin}/bottom version")
  end
end
