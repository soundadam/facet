# facet

**给 fingerprint-chromium 管多账号的命令行工具**：一个账号一套指纹、cookie、
代理和语言时区，各开各的窗口。

[![CI](https://github.com/soundadam/facet/actions/workflows/ci.yml/badge.svg)](https://github.com/soundadam/facet/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/soundadam/facet)](https://github.com/soundadam/facet/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium) 的
指纹全靠启动参数：种子、平台、品牌、时区、语言、代理，每次手敲，还要自己
记哪个账号用了哪个种子。facet 把这些存成 profile，`facet open` 一条命令按
profile 拼好参数启动。支持 macOS 和 Linux，Go 编写。

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
  Service），`profile.toml` 里只有用户名。
- **默认自洽**：平台默认与本机一致，此时 GPU 不伪装，WebGL 报的就是真显卡；
  设了代理却没设时区会提醒你，因为出口 IP 和时区对不上是最常见的破绽。

## 快速开始

先装好 fingerprint-chromium（142 及以上，推荐 148），macOS 放到
`/Applications/Chromium.app`，Linux 把 `fingerprint-chromium` 放进 PATH。
放在别处就设 `FACET_CHROMIUM`，指向 `.app` 或可执行文件都行。

```sh
brew trust --tap soundadam/tap
brew install --cask soundadam/tap/facet

# 一个账号一个 profile；代理 URL 里不写密码，facet 会问你，然后存进钥匙串
facet new shop-us --proxy socks5://alice@203.0.113.7:1080 \
  --timezone America/New_York --lang en-US

facet open shop-us                       # 启动
facet open 1 https://example.com/        # 已在运行时，在同一个浏览器里开新窗口
facet                                    # 列出所有 profile
facet stop shop-us
```

profile 可以用序号（`1`、`#1`）、名字（不分大小写）或 ID 指定。

## 命令

| 命令 | 作用 |
| --- | --- |
| `facet` / `facet ls` | 列出 profile：序号、名字、平台、代理、时区、运行状态 |
| `facet new NAME` | 新建 profile，生成种子和硬件并发数 |
| `facet set PROFILE` | 修改代理、时区、语言、品牌、备注、启动页，或用 `--name` 改名；种子和平台不能改 |
| `facet show PROFILE` | 显示全部设置，以及钥匙串里有没有代理密码 |
| `facet open PROFILE [URL...]` | 启动；已运行就开新窗口。`--foreground` 留在前台看日志 |
| `facet stop PROFILE` | 关闭浏览器 |
| `facet rm PROFILE` | 把 profile 目录（含 cookie）移进废纸篓，并删掉钥匙串里的密码 |
| `facet update` / `facet version` | 自更新 / 版本 |

`new` 和 `set` 共用这些选项：

- `--proxy`：`http`、`https`、`socks5`、`socks5h`，或 `direct`。
- `--timezone`：IANA 时区，如 `Asia/Tokyo`；传 `""` 清除。
- `--lang`：界面语言和 `navigator.language`。
- `--accept-lang`：不设时由 `--lang` 推出，如 `en-US` → `en-US,en`。
- `--brand`、`--brand-version`：UA 和 client hints 里的品牌，Chrome、Edge、
  Opera 或 Vivaldi。
- `--platform-version`：client hints 里的系统版本。
- `--notes`：备注。
- `--url`：启动页，可重复。

`--platform`（`macos`、`windows`、`linux`）只能在 `new` 时指定。

## 数据放在哪

```text
~/Library/Application Support/facet/      macOS（Linux 是 ~/.config/facet/）
  profiles/<id>/profile.toml              设置，可以手改
  profiles/<id>/user-data/                Chromium 的 --user-data-dir
  profiles/<id>/run.json                  运行时存在：supervisor、浏览器 PID 和桥地址
  profiles/<id>/facet.log                 上一次运行的日志，启动失败先看这里
```

设 `FACET_HOME` 可以换位置。代理密码在钥匙串里，服务名 `facet`，账户名
`proxy:<id>`。

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

- 浏览器下载的 Chromium.app 带隔离属性，macOS 会把它放进 App Translocation
  的随机路径里运行。用 facet 前先打开一次并在「隐私与安全性」里允许，或者
  自己去掉：

  ```sh
  xattr -dr com.apple.quarantine /Applications/Chromium.app
  ```

- facet 本身没有经过 Apple 公证。Homebrew 装的直接能用；从 Releases 下载的
  同样需要去掉隔离属性。
- 指纹只是一层。同一个出口 IP 登多个账号、账号间的行为关联，facet 管不了。

## 开发

```sh
make build        # bin/facet
make check        # gofmt、vet、测试
make ci           # CI 跑的全部内容，含 -race
make package VERSION=0.1.0   # 各平台压缩包到 dist/，需要 GNU tar
```

测试不会启动真浏览器：桥对着进程内的 HTTP / SOCKS5 代理测，启动器对着假的
内核脚本测。发版前在本机用真的 fingerprint-chromium 走一遍
`new`、`open`、再次 `open`、`stop`。

## 许可

MIT，见 [LICENSE](LICENSE)。fingerprint-chromium 是独立项目，有自己的许可，
facet 不分发它。
