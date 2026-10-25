# frozen_string_literal: true

# The release workflow's homebrew job fills in @VERSION@ and the SHA-256 of each
# archive and writes the result to Casks/facet.rb in soundadam/homebrew-tap.
cask "facet" do
  version "@VERSION@"

  on_macos do
    on_arm do
      sha256 "@MACOS_AARCH64_SHA256@"

      url "https://github.com/soundadam/facet/releases/download/v#{version}/facet-#{version}-macos-aarch64.tar.gz"

      binary "facet-#{version}-macos-aarch64/facet"
    end
    on_intel do
      sha256 "@MACOS_X86_64_SHA256@"

      url "https://github.com/soundadam/facet/releases/download/v#{version}/facet-#{version}-macos-x86_64.tar.gz"

      binary "facet-#{version}-macos-x86_64/facet"
    end

    # Ad-hoc signed and not notarized: clear the quarantine bit so Gatekeeper
    # treats it like a file that facet update put in place.
    postflight_steps do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{staged_path}}"]
    end
  end
  on_linux do
    on_arm do
      sha256 "@LINUX_AARCH64_SHA256@"

      url "https://github.com/soundadam/facet/releases/download/v#{version}/facet-#{version}-linux-aarch64.tar.gz"

      binary "facet-#{version}-linux-aarch64/facet"
    end
    on_intel do
      sha256 "@LINUX_X86_64_SHA256@"

      url "https://github.com/soundadam/facet/releases/download/v#{version}/facet-#{version}-linux-x86_64.tar.gz"

      binary "facet-#{version}-linux-x86_64/facet"
    end
  end

  name "facet"
  desc "Manage fingerprint-chromium profiles for multiple accounts"
  homepage "https://github.com/soundadam/facet"

  livecheck do
    url :url
    strategy :github_latest
  end

  caveats <<~EOS
    facet is MIT licensed.

    facet launches fingerprint-chromium but does not bundle it. Install
    version 142 or later as /Applications/Chromium.app (macOS) or put
    fingerprint-chromium on PATH (Linux), or point FACET_CHROMIUM at it.
  EOS
end
