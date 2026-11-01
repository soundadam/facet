# facet

**给 fingerprint-chromium 管多账号的终端工具**：一个账号一套指纹、cookie、
代理和语言时区，各开各的窗口。

[![CI](https://github.com/soundadam/facet/actions/workflows/ci.yml/badge.svg)](https://github.com/soundadam/facet/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/soundadam/facet)](https://github.com/soundadam/facet/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium) 的
指纹全靠启动参数：种子、平台、品牌、时区、语言、代理，每次手敲，还要自己
记哪个账号用了哪个种子。facet 把这些存成 profile，在终端里直接运行 `facet`
就能列出、新建、编辑、打开和关闭它们；也可以用 `facet open` 这类子命令写脚本。
支持 macOS、Linux 和 Windows，Go 编写。

## 为什么是 facet

- **身份固定**：种子在创建时生成并写进 `profile.toml`，之后不变。canvas、
  音频、字体都由它派生，同一个 profile 重启后指纹一致，不同 profile 之间
  互不相同。
- **带密码的代理也能用**：Chromium 自己不支持代理认证。代理带用户名时，
  facet 在本机起一个只听 `127.0.0.1` 的桥，由它向上游认证（HTTP、HTTPS、
  SOCKS5 都行），浏览器只连这个桥。
- **代理断了就不出网**：桥由 supervisor 进程托管。supervisor 意外退出时
  桥一起消失，浏览器的请求直接失败，不会绕开代理用本机 IP 出去。facet
  会把这种状态标成 `running, bridge down`。
- **密码不落盘**：代理密码存在系统钥匙串（macOS Keychain / Linux Secret
  Service / Windows 凭据管理器），`profile.toml` 里只有用户名。
- **数据都在一个目录里**：cookie、登录状态、缓存全在 profile 自己的目录下，
  `facet wipe` 清掉浏览数据但保留指纹，`facet rm` 整个移进废纸篓。
- **默认自洽**：平台默认与本机一致，此时 GPU 不伪装，WebGL 报的就是真显卡；
  设了代理却没设时区会提醒你，因为出口 IP 和时区对不上是最常见的破绽。

## 快速开始

facet 不自带浏览器，内核用上游
[fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium/releases)
（142 及以上）。`facet kernel install` 从上游 GitHub Releases 下载本机对应的
构建，按 GitHub 公布的 SHA-256 校验后解压到 facet 自己的目录；第一次
`facet open` 时没找到内核，facet 也会问你要不要下载。上游目前有 macOS
（Apple 芯片）、Linux x86_64 和 Windows x64 的构建，Intel Mac 没有。

已经自己装过的也能用：macOS 的 `/Applications/Chromium.app`、Linux PATH 里的
`fingerprint-chromium`、Windows 安装包装到的 `%LOCALAPPDATA%\Chromium\Application\`
都会被找到；放在别处就设 `FACET_CHROMIUM`，指向 `.app` 或可执行文件都行。
上游的文件名叫 `ungoogled-chromium_…`，但它是 fingerprint-chromium；官方
Chromium 或 Chrome 不认 `--fingerprint` 参数，不能用。

facet 本身在 macOS 和 Linux 上用 Homebrew 装；Windows 从
[Releases](https://github.com/soundadam/facet/releases) 下载
`facet-<版本>-windows-x86_64.zip`（ARM 机器用 `aarch64`），把 `facet.exe`
放进 PATH，之后 `facet update` 自更新。

```sh
brew trust --tap soundadam/tap
brew install --cask soundadam/tap/facet
facet kernel install                     # 下载内核；facet kernel 看当前版本和上游最新版

# 一个账号一个 profile；代理 URL 里不写密码，facet 会问你，然后存进钥匙串
facet new shop-us --proxy socks5://alice@203.0.113.7:1080 \
  --timezone America/New_York --lang en-US
facet new shop-lv --timezone "Las Vegas" --lang 中文   # 城市名和语言名也行

facet                                    # 交互界面
facet open shop-us                       # 启动
facet open 1 https://example.com/        # 已在运行时，在同一个浏览器里开新窗口
facet stop shop-us
```

profile 可以用序号（`1`、`#1`）、名字（不分大小写）或 ID 指定。

## 交互界面

在终端里直接运行 `facet`（或 `facet ui`）。上半部分是 profile 列表和运行
状态，每 2 秒刷新；下半部分是选中 profile 的设置。

| 按键 | 作用 |
| --- | --- |
| `↑` `↓` / `j` `k` | 选择 |
| `enter` / `o` | 打开；已在运行就开新窗口 |
| `s` | 关闭 |
| `n` / `e` | 新建 / 编辑 |
| `w` | 清浏览数据，保留指纹（要确认） |
| `d` | 删除 profile（要确认） |
| `r` | 重新读取 |
| `q` | 退出界面，已打开的浏览器继续运行 |

新建和编辑是同一个表单：`tab` 或 `↓` 换下一项，`enter` 在最后一项保存，
`ctrl+s` 随时保存，`esc` 取消。时区和语言框是空的或填着常用值时，按 `←` `→`
在常用值里轮换（拉斯维加斯、纽约、新加坡、东京……；en-US、zh-CN、ja-JP……）；
也可以直接输入 IANA 名、城市名（`Las Vegas`、`东京`）或语言名（`中文`），
输入开头后按 `→` 补全。框下面实时显示换算结果、UTC 偏移和当地时间。密码框
留空表示保留钥匙串里原有的密码。

输出不是终端时（比如接管道），`facet` 退回成 `facet ls` 的表格。

## 命令

| 命令 | 作用 |
| --- | --- |
| `facet` / `facet ui` | 交互界面；不在终端里时同 `facet ls` |
| `facet ls` | 列出 profile：序号、名字、平台、代理、时区、运行状态 |
| `facet new NAME` | 新建 profile，生成种子和硬件并发数 |
| `facet set PROFILE` | 修改代理、时区、语言、品牌、备注、启动页，或用 `--name` 改名；种子和平台不能改 |
| `facet show PROFILE` | 显示全部设置，以及钥匙串里有没有代理密码 |
| `facet open PROFILE [URL...]` | 启动；已运行就开新窗口。`--foreground` 留在前台看日志 |
| `facet stop PROFILE` | 关闭浏览器 |
| `facet wipe PROFILE` | 把 cookie、登录、历史、缓存移进废纸篓；种子、代理、时区不变，下次打开像新装的浏览器 |
| `facet rm PROFILE` | 把 profile 目录（含 cookie）移进废纸篓，并删掉钥匙串里的密码 |
| `facet zones` | 列出常用时区（含当前 UTC 偏移和当地时间、对应城市）和语言 |
| `facet kernel` | 显示正在用的内核、它从哪来，以及上游有没有更新的本机构建 |
| `facet kernel install [VERSION\|FILE]` | 从上游 Releases 下载并校验内核；`VERSION` 可写 `148` 这样的大版本，`FILE` 装自己下好的压缩包。要先关掉所有 profile |
| `facet update` / `facet version` | 自更新 / 版本 |

`new` 和 `set` 共用这些选项：

- `--proxy`：`http`、`https`、`socks5`、`socks5h`，或 `direct`。
- `--timezone`：IANA 时区，如 `Asia/Tokyo`。也认常用的城市和国家名，中英文
  都行（`Las Vegas`、`纽约`、`Singapore`、`日本`），存成对应的 IANA 名；
  `facet zones` 列出常用的。IANA 里没有 `America/Las_Vegas`，拉斯维加斯属于
  `America/Los_Angeles`，写前者 facet 会自动换。传 `""` 清除。
- `--lang`：界面语言和 `navigator.language`，BCP 47 标签如 `en-US`、`zh-CN`、
  `ja-JP`。大小写和下划线会规范化（`en_us` → `en-US`），也认 `English`、
  `中文`、`繁體中文`、`日本語` 这类名字。
- `--accept-lang`：不设时由 `--lang` 推出，如 `en-US` → `en-US,en`。
- `--brand`、`--brand-version`：UA 和 client hints 里的品牌，Chrome、Edge、
  Opera 或 Vivaldi。
- `--platform-version`：client hints 里的系统版本。
- `--notes`：备注。
- `--url`：启动页，可重复。

`--platform`（`macos`、`windows`、`linux`）只能在 `new` 时指定。

## 数据放在哪

```text
~/Library/Application Support/facet/      macOS（Linux 是 ~/.config/facet/，Windows 是 %AppData%\facet\）
  profiles/<id>/profile.toml              设置，可以手改
  profiles/<id>/user-data/                Chromium 的 --user-data-dir：cookie、登录、历史、扩展
  profiles/<id>/cache/                    Chromium 的 --disk-cache-dir：HTTP 和代码缓存
  profiles/<id>/run.json                  运行时存在：supervisor、浏览器 PID 和桥地址
  profiles/<id>/facet.log                 上一次运行的日志，启动失败先看这里
```

`facet kernel install` 装的内核在 `~/Library/Application Support/facet/chromium/`
（Linux 是 `~/.local/share/facet/chromium/`，Windows 是 `%LOCALAPPDATA%\facet\chromium\`），
facet 找内核时先看这里。设 `FACET_HOME` 可以换位置，内核也跟着放到
`$FACET_HOME/chromium/`。代理密码在钥匙串里，服务名 `facet`，账户名
`proxy:<id>`。Windows 上 `wipe` 和 `rm` 不进回收站，而是移到数据目录下的
`trash\`，确认不要了自己删。

profile 目录以外，Chromium 还会碰这几处：

- `~/Library/Caches/facet/profiles/<id>/`：macOS 上 Chromium 默认把缓存放
  这里。facet 用 `--disk-cache-dir` 把缓存挪回 profile 目录，这里只剩空
  目录，`wipe` 和 `rm` 会一并删掉。
- 钥匙串里的 `Chromium Safe Storage`：Chromium 用这把钥匙加密各 profile
  的 cookie 和密码。所有 profile 共用一把，网站看不到它，只关系到本机谁能
  解密这些文件。
- `~/Library/Application Support/Chromium/`：直接双击打开 Chromium.app 时
  用的默认 profile，不归 facet 管，也不会被 facet 用到。

## 运行方式

`facet open` 在后台起一个 `facet run <id>` 作为 supervisor，等它报告浏览器
已启动再返回。supervisor 负责：

1. 代理带用户名时，起本地桥；否则把代理地址直接交给 Chromium。
2. 按 profile 拼参数启动 Chromium：`--fingerprint`、`--fingerprint-platform`、
   `--timezone`、`--lang`、`--accept-lang`、`--proxy-server` 等。设了代理就加
   `--disable-quic` 和 `--disable-non-proxied-udp`，避免 QUIC 和 WebRTC 绕开代理。
3. 写 `run.json`，浏览器退出后清掉；收到 `facet stop` 时先让浏览器正常退出，
   10 秒后还没退就强制结束。

窗口标题带 `[序号] 名字`，多个账号同时开着也分得清。

## 注意

- 连不上 GitHub 时，用别的办法（镜像、网盘）下好上游的压缩包：macOS 的
  `.dmg`、Linux 的 `.tar.xz`、Windows 的 `.zip`，再
  `facet kernel install <文件>`。能查到上游的 SHA-256 就校验，查不到会提示
  未校验。下载走 `https_proxy` 等环境变量里的代理。Linux 解压 `.tar.xz` 需要
  系统里有 `xz`。
- `facet kernel install` 装的 Chromium.app 没有隔离属性，可以直接用。自己用
  浏览器下载的带隔离属性，macOS 会把它放进 App Translocation 的随机路径里
  运行。用 facet 前先打开一次并在「隐私与安全性」里允许，或者自己去掉：

  ```sh
  xattr -dr com.apple.quarantine /Applications/Chromium.app
  ```

- facet 本身没有经过 Apple 公证。Homebrew 装的直接能用；从 Releases 下载的
  同样需要去掉隔离属性。
- Windows 版在 CI 上编译并跑全部测试，但还没在真机上配真内核完整走过一遍，
  遇到问题请提 issue。`facet.exe` 没有代码签名，第一次运行 SmartScreen 可能
  拦一下，点「仍要运行」。
- 指纹只是一层。同一个出口 IP 登多个账号、账号间的行为关联，facet 管不了。

## 开发

```sh
make build        # bin/facet
make check        # gofmt、vet、测试
make ci           # CI 跑的全部内容，含 -race
make package VERSION=0.2.0   # 各平台压缩包到 dist/，需要 GNU tar 和 zip
```

测试不会启动真浏览器：桥对着进程内的 HTTP / SOCKS5 代理测，启动器对着假的
内核脚本测。发版前在本机用真的 fingerprint-chromium 走一遍
`new`、`open`、再次 `open`、`stop`。

## 许可

MIT，见 [LICENSE](LICENSE)。fingerprint-chromium 是独立项目，有自己的许可，
facet 不分发它。
