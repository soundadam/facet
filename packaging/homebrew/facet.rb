# frozen_string_literal: true

# The release workflow's homebrew job fills in @VERSION@ and the SHA-256 of each
# archive and writes the result to Casks/facet.rb in soundadam/homebrew-tap.
cask "facet" do
  arch arm: "aarch64", intel: "x86_64"

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
    # treats it like a file that facet update put in place. Then download
    # fingerprint-chromium unless one is already usable. Only download: the
    # sandbox these steps run in cannot mount its dmg, so facet unpacks it
    # the first time it needs a kernel. The sandbox also points HOME at a
    # scratch directory; FACET_ACCOUNT_HOME has facet use the account's. A
    # failure (GitHub unreachable, no build for Intel Macs) leaves facet
    # installed and the caveats say how to finish.
    postflight_steps do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{staged_path}}"]
      run "{{staged_path}}/facet-#{version}-macos-#{arch}/facet",
          args:           ["kernel", "install", "--if-missing", "--download-only"],
          must_succeed:   false,
          print_stdout:   true,
          network_access: true,
          env:            { "FACET_ACCOUNT_HOME" => "1" },
          writable_paths: ["Library/Application Support/facet"],
          writable_base:  :home
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

    postflight_steps do
      run "{{staged_path}}/facet-#{version}-linux-#{arch}/facet",
          args:           ["kernel", "install", "--if-missing", "--download-only"],
          must_succeed:   false,
          print_stdout:   true,
          network_access: true,
          env:            { "FACET_ACCOUNT_HOME" => "1" },
          writable_paths: [".local/share/facet"],
          writable_base:  :home
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

    facet launches fingerprint-chromium but does not bundle it. Installing
    facet downloads it from github.com/adryfish/fingerprint-chromium unless
    one is already usable, and facet unpacks it the first time it needs a
    kernel. If the download failed, run

      facet kernel install

    or install an archive fetched some other way with
    `facet kernel install FILE`. `facet kernel` shows the kernel in use.
  EOS
end
