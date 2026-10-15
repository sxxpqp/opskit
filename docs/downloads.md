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
- 首页使用固定版本链接。更新首页版本时修改 `downloads.json` 的 `version`，运行 `python3 scripts/download_links.py --readme`；不要填写尚未发布完成的版本。
- 可单独生成某个版本的下载表：`python3 scripts/download_links.py --tag v0.1.5`。
- 使用固定版本而非 `latest`，避免代理缓存让主 / 备用入口对应不同版本。已有 Release 的说明不会自动改写；v0.1.5 已单独补充同版本备用下载表。

当前已验证 v0.1.5 校验文件和 Release 配置模板可匿名下载，Linux amd64 安装包已实际下载并通过 SHA-256 校验。Nexus 是代理缓存，首次获取仍依赖上游；未缓存文件在上游不可达时可能下载失败，不能将它视作独立离线镜像。源码代理当前请求返回错误，因此首页模板备用链接使用已验证的 Release 代理路径。
