# Nexus 备用下载

首页同时提供 GitHub 与 Nexus 下载。安装包、模板和校验文件使用相同版本标签，按系统与 CPU 架构分别选择。

## 当前仓库

| Nexus 仓库 | 上游 | 用途 |
|---|---|---|
| `raw-github` | `https://github.com` | Release 可执行文件、配置模板、checksums.txt |
| `raw-githubusercontent` | `https://raw.githubusercontent.com` | 源码中的文件；不能直接代理 Release 附件 |

下载基础地址集中在根目录 `downloads.json` 的 `nexus_release_base` 字段；它必须是代理 GitHub 的仓库地址，不要填源码代理地址。无需上传账号，因为这是 raw proxy，文件由 Nexus 在用户下载时回源并缓存。原理见 [Sonatype Raw 仓库文档](https://help.sonatype.com/en/raw-repositories.html)。

## 路径与示例

```text
https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/
  sxxpqp/opskit/releases/download/<版本>/<文件名>
```

例如 Linux x86_64 的 v0.1.5：

```sh
curl -fL --connect-timeout 10 \
  https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.5/opskit_linux_amd64 \
  -o opskit_linux_amd64
curl -fL --connect-timeout 10 \
  https://nexus.ihome.sxxpqp.top:8443/repository/raw-github/sxxpqp/opskit/releases/download/v0.1.5/checksums.txt \
  -o checksums.txt
# 只校验本次下载的文件，校验成功后再重命名运行：
awk '$2 == "opskit_linux_amd64"' checksums.txt | sha256sum -c -
mv opskit_linux_amd64 opskit
chmod +x opskit
./opskit version
```

macOS 可用 `shasum -a 256`，Windows PowerShell 可用 `Get-FileHash -Algorithm SHA256`，与同版本校验文件比较。

## 发布与维护

- tag 发布时，工作流按实际版本生成主 / 备用下载表并加入 Release 说明。不会修改 Nexus 仓库设置，也不预先上传安装包。
- 正式 Release 发布成功后，工作流直接查询 GitHub 的最新已发布版本，更新 `downloads.json`，同步 README 顶部版本摘要、近期更新入口与下载表，并提交到 main。较旧 tag 即使晚完成，也不会把首页降级为旧版本。
- 首页直接指向 `/releases/download/<版本>/...`，不使用 Nexus 可能缓存的 `/releases/latest/download/...`。新版本是新地址，不会命中旧版本文件的缓存。
- 页面已打开时不会实时变化，新版本发布后刷新仓库首页即可。已打开的历史 Release 始终下载其对应版本。
- 自动更新失败时，Actions 会显示失败；可在修复后重新运行 `update-downloads` 作业。手动修复也可运行 `python3 scripts/download_links.py --tag <已发布版本> --readme --update-version`。
- 可单独生成某个版本的下载表：`python3 scripts/download_links.py --tag v0.1.5`。
- 使用固定版本而非 `latest`，避免代理缓存让主 / 备用入口对应不同版本。已有 Release 的说明不会自动改写；v0.1.5 已单独补充同版本备用下载表。

当前已验证 v0.1.5 校验文件和 Release 配置模板可匿名下载，Linux amd64 安装包已实际下载并通过 SHA-256 校验。Nexus 是代理缓存，首次获取仍依赖上游；未缓存文件在上游不可达时可能下载失败，不能将它视作独立离线镜像。源码代理当前请求返回错误，因此首页模板备用链接使用已验证的 Release 代理路径。

## “最新版本”与代理缓存

最新指 GitHub 标记的最新正式 Release，构建通过并成功发布后才切换，不包括 PR 或 main 的临时构建。链接中的明确版本保证不因 Nexus 的 latest 路径缓存而下载到上一版本；相同版本仍可由 Nexus 缓存提供，这不会变成旧版本。不要覆盖已有版本的资产，修改后发布新 tag。

此方案不修改 Nexus 缓存设置，不宣称每次下载都绕过缓存，也不依赖 `?timestamp=` 等查询参数。GitHub、Nexus、网络不可用时仍可能下载失败。
