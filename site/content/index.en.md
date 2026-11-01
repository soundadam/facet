---
title: facet
excerpt: A free, open-source fingerprint browser manager for the terminal. Each account gets its own fingerprint, cookies, proxy and time zone, in its own window.
weight: 50
presentation:
  category: Fingerprint browser / TUI · macOS + Linux + Windows
  label: Install with Homebrew
  command:
    - brew trust --tap soundadam/tap
    - brew install --cask soundadam/tap/facet
  note: Free and MIT licensed. No account, no cloud, no telemetry.
registry:
  github: soundadam/facet
  homebrew: soundadam/tap/facet
  docs: https://github.com/soundadam/facet#readme
release:
  version: "0.2.0"
  license: MIT
modules:
  - type: promo
    description: Running many accounts means giving each one its own computer. {title} builds on the open-source fingerprint-chromium engine. It stores the fingerprint seed, platform, proxy, time zone and language as a profile that you create, open and close from a terminal interface, and no profile sees another's cookies or logins.
    line: "{title} {version} is a free, {license}-licensed tool for macOS, Linux and Windows, on Intel and ARM. No sign-up and no subscription."
    actions:
      - label: Docs (Chinese)
        url: "{docs}"
      - label: GitHub
        url: "https://github.com/{github}"

  - type: snapshot
    title: Why facet
    body: Commercial fingerprint browsers charge a monthly fee per profile and keep your accounts and settings in their cloud. fingerprint-chromium is a free, open-source engine, but its fingerprint is set entirely by launch flags, typed by hand every time, and you have to remember which seed went with which account. {title} is the layer in between. Settings are local TOML files, proxy passwords go into the system keychain, and it is one binary that sends nothing anywhere.
    note: "{title} does not ship the browser engine. Install fingerprint-chromium 142 or later from its Releases first. A fingerprint is only one layer: several accounts behind one exit IP, or behaviour that links accounts, are beyond what {title} can fix."
    metrics:
      - value: "$0"
        label: No per-profile fee and no profile limit
      - value: "3"
        label: "Platforms to present as: macOS, Windows, Linux"
      - value: "6"
        label: "Release builds: macOS, Linux and Windows, each on x86_64 and ARM"
      - value: "{version}"
        label: "Current release · {license}"

  - type: section
    title: Every account in one terminal
    index: 1/3
    lede: "Run `facet` to open the interactive view. The top half lists profiles and whether they are running, refreshed every two seconds; the bottom half shows the selected profile's fingerprint settings. `enter` opens, `s` stops, `n` creates, `e` edits and `w` wipes browsing data while keeping the fingerprint. For scripts, `facet new`, `facet open` and `facet stop` work on their own."

  - type: media
    image: tui.svg
    alt: facet's interactive view in a terminal, listing three profiles with their proxies and time zones, with the selected profile's platform, seed, brand, proxy, time zone and language below
    caption: "Running `facet`: three profiles, each with its own proxy and time zone, and the full fingerprint settings of the selected one."

  - type: section
    title: The same identity after every restart
    index: 2/3
    lede: The seed is generated when a profile is created, written to `profile.toml` and never changed. Canvas, audio and fonts derive from it, and hardware concurrency is fixed at creation too, so a profile has the same fingerprint every time it opens, and no two profiles share one. The platform defaults to your own, so WebGL reports the real GPU. If you set a proxy but no time zone, {title} warns you, because an exit IP that disagrees with the time zone is the most common giveaway.

  - type: section
    title: Proxies with passwords, and no leak when they drop
    index: 3/3
    lede: Chromium cannot authenticate to a proxy on its own. When a proxy has a username, {title} runs a local bridge listening only on `127.0.0.1` that authenticates upstream over HTTP, HTTPS or SOCKS5. A supervisor owns the bridge; if the supervisor exits, the bridge goes with it and the browser's requests fail instead of leaving through your own IP. QUIC and non-proxied UDP are switched off too, so WebRTC has no side door.
---
