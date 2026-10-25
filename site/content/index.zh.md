---
title: facet
excerpt: 免费开源的指纹浏览器管理器，在终端里管多账号：一个账号一套指纹、cookie、代理和时区，各开各的窗口。
weight: 50
presentation:
  category: 指纹浏览器 / TUI · macOS + Linux
  label: 用 Homebrew 安装
  command:
    - brew trust --tap soundadam/tap
    - brew install --cask soundadam/tap/facet
  note: 免费，MIT 开源。没有账号，没有云端，没有遥测。
registry:
  github: soundadam/facet
  homebrew: soundadam/tap/facet
  docs: https://github.com/soundadam/facet#readme
release:
  version: "0.1.0"
  license: MIT
modules:
  - type: promo
    description: 多账号运营要的是每个账号一台「独立的电脑」。{title} 基于开源内核 fingerprint-chromium，把指纹种子、平台、代理、时区、语言存成 profile，在终端界面里新建、打开、关闭，每个 profile 的 cookie 和登录状态互不相通。
    line: "{title} {version} 是 {license} 开源的免费工具，支持 macOS 和 Linux（Intel 与 ARM），不需要注册，不收订阅费。"
    actions:
      - label: 文档
        url: "{docs}"
      - label: GitHub
        url: "https://github.com/{github}"

  - type: snapshot
    title: 为什么做 facet
    body: 商业指纹浏览器按 profile 数收月费，账号和配置存在别人的云上。fingerprint-chromium 是免费的开源内核，但指纹全靠启动参数，每次手敲，还要自己记哪个账号用了哪个种子。{title} 补上中间这一层：配置是本地的 TOML 文件，代理密码进系统钥匙串，一个二进制文件，什么都不往外传。
    note: "{title} 不分发浏览器内核，先从 fingerprint-chromium 的 Releases 装好 142 及以上版本。指纹只是一层：同一个出口 IP 登多个账号、账号之间的行为关联，{title} 管不了。"
    metrics:
      - value: "0 元"
        label: 不按 profile 收费，不限数量
      - value: "3"
        label: 可伪装的平台：macOS、Windows、Linux
      - value: "4"
        label: 发布平台：macOS 与 Linux，各有 x86_64 和 ARM
      - value: "{version}"
        label: "当前版本 · {license}"

  - type: section
    title: 在终端里管所有账号
    index: 1/3
    lede: "直接运行 `facet` 打开交互界面：上半部分是 profile 列表和运行状态，每 2 秒刷新；下半部分是选中 profile 的指纹设置。`enter` 打开，`s` 关闭，`n` 新建，`e` 编辑，`w` 清掉浏览数据但保留指纹。要写脚本，`facet new`、`facet open`、`facet stop` 这些子命令都能单独用。"

  - type: media
    image: tui.svg
    alt: 终端里的 facet 交互界面，列出三个 profile 的代理和时区，下方显示选中 profile 的平台、种子、品牌、代理、时区和语言
    caption: "运行 `facet` 的样子：三个 profile 各有自己的代理和时区，选中的那一个显示完整指纹设置。"

  - type: section
    title: 身份固定，重启也一样
    index: 2/3
    lede: 种子在创建 profile 时生成并写进 `profile.toml`，之后不变。canvas、音频、字体都由它派生，硬件并发数也在创建时定下，同一个 profile 每次打开指纹一致，不同 profile 之间互不相同。平台默认和本机一致，WebGL 报的就是真显卡；设了代理却没设时区，{title} 会提醒你，出口 IP 和时区对不上是最常见的破绽。

  - type: section
    title: 带密码的代理，断了也不漏 IP
    index: 3/3
    lede: Chromium 自己不支持代理认证。代理带用户名时，{title} 在本机起一个只听 `127.0.0.1` 的桥，由它向上游认证，HTTP、HTTPS、SOCKS5 都行。桥由 supervisor 托管，supervisor 一退出桥就消失，浏览器的请求直接失败，不会绕开代理用本机 IP 出去；同时关掉 QUIC 和非代理 UDP，WebRTC 也走不了旁路。
---
