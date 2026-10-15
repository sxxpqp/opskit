#!/usr/bin/env python3
"""Render version-pinned primary and Nexus download links for README and releases."""
import argparse
import json
import re
from pathlib import Path
from urllib.parse import quote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
START = '<!-- DOWNLOAD_LINKS_START -->'
END = '<!-- DOWNLOAD_LINKS_END -->'


def render(config, tag):
    if not re.fullmatch(r'v[0-9][A-Za-z0-9._-]*', tag):
        raise ValueError('version must be a v-prefixed version tag')
    repo = config['github_repository']
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repo):
        raise ValueError('invalid GitHub repository')
    base = config['nexus_release_base'].rstrip('/')
    url = urlsplit(base)
    if url.scheme != 'https' or not url.netloc or url.username or url.password or url.query or url.fragment:
        raise ValueError('Nexus base must be an HTTPS repository URL without credentials or query')
    suffix = f'{repo}/releases/download/{quote(tag)}/'
    primary = 'https://github.com/' + suffix
    mirror = base + '/' + suffix
    lines = [f'### {tag} 下载：GitHub / Nexus 备用', '',
             '同一行的主链接与备用链接指向同一版本。按系统和 CPU 架构下载，无需解压。', '',
             '| 系统 | CPU 架构 | GitHub | Nexus 备用 |', '|---|---|---|---|']
    for os_name, label in [('windows', 'Windows'), ('linux', 'Linux'), ('darwin', 'macOS')]:
        for arch, cpu in [('amd64', 'x86_64 / amd64'), ('arm64', 'ARM64 / aarch64')]:
            filename = f'opskit_{os_name}_{arch}' + ('.exe' if os_name == 'windows' else '')
            lines.append(f'| {label} | {cpu} | [下载]({primary}{filename}) | [备用下载]({mirror}{filename}) |')
    lines += ['', f'校验文件：[GitHub]({primary}checksums.txt) · [Nexus]({mirror}checksums.txt)', '',
              f'配置模板：[GitHub]({primary}config.example.yaml) · [Nexus]({mirror}config.example.yaml)', '',
              'Nexus 为上游代理缓存，首次请求需回源；不是独立备份。首页链接随正式发布自动更新为最新版本的固定路径，避免 latest 缓存造成版本混淆。']
    return '\n'.join(lines) + '\n'


def update_homepage(content, config, tag, links):
    before, separator, rest = content.partition(START)
    _, end, after = rest.partition(END)
    if not separator or not end:
        raise ValueError('README download markers missing')
    content = before + START + '\n' + links + END + after
    release_url = f"https://github.com/{config['github_repository']}/releases/tag/{quote(tag)}"
    history_header = '| 功能版本 | 变化 |\n|---|---|'
    if history_header not in content:
        raise ValueError('README release history table missing')
    pattern = r'^\| \[' + re.escape(tag) + r'\]\([^)]*\) \| (.*?) \|$'
    match = re.search(pattern, content, re.MULTILINE)
    if match:
        highlights = match.group(1)
    else:
        highlights = '新版本已发布，详细变化见对应 Release 说明'
        content = content.replace(history_header,
                                  history_header + f'\n| [{tag}]({release_url}) | {highlights} |', 1)
    summary = (f'**当前最新正式版：[{tag}]({release_url})**\n\n'
               f'{highlights}。\n\n'
               '→ [选择系统与架构下载](#下载) · [使用 Nexus 备用入口](#下载) · '
               '[查看近期更新](#近期更新)\n')
    start, finish = '<!-- LATEST_RELEASE_START -->', '<!-- LATEST_RELEASE_END -->'
    before, separator, rest = content.partition(start)
    _, end, after = rest.partition(finish)
    if not separator or not end:
        raise ValueError('README latest release markers missing')
    return before + start + '\n' + summary + finish + after


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', help='override downloads.json version for a release')
    parser.add_argument('--output', type=Path, help='write Markdown to this file')
    parser.add_argument('--readme', action='store_true', help='update the marked README download table')
    parser.add_argument('--update-version', action='store_true', help='persist the verified published version in downloads.json')
    args = parser.parse_args()
    config = json.loads((ROOT / 'downloads.json').read_text())
    tag = args.tag or config['version']
    text = render(config, tag)
    if args.update_version:
        if not args.tag:
            parser.error('--update-version requires --tag')
        config['version'] = tag
        (ROOT / 'downloads.json').write_text(json.dumps(config, indent=2, ensure_ascii=False) + '\n')
    if args.readme:
        path = ROOT / 'README.md'
        content = path.read_text()
        path.write_text(update_homepage(content, config, tag, text))
    elif args.output:
        args.output.write_text(text)
    else:
        print(text, end='')


if __name__ == '__main__':
    main()
