#!/usr/bin/env python3
"""一键在国内 new-api 创建「海外二级网关」渠道（途径 A：渠道套娃）。

把海外 new-api（api.ueejavelin.org）当作一条上游渠道接入，出站走 mihomo 代理
（socks5h://mihomo:7890），模型列表为 Claude + GPT（DeepSeek 暂不接入，国内可直连官方）。

纯标准库实现，无第三方依赖，服务器上直接 `python3 scripts/create-overseas-channel.py` 即可。

鉴权（new-api 管理接口）：
  - Authorization: <系统访问令牌>      # 个人设置 → 生成系统访问令牌；注意不带 Bearer
  - New-Api-User: <用户ID>            # 令牌所属用户 id，须为管理员（role>=10）

用法（环境变量 或 命令行参数，参数优先）：
  export NEWAPI_BASE=http://127.0.0.1:3000
  export NEWAPI_TOKEN=xxxxxxxx          # 系统访问令牌
  export NEWAPI_USER_ID=1               # 管理员用户 id
  python3 scripts/create-overseas-channel.py

  # 或：
  python3 scripts/create-overseas-channel.py \
      --base http://127.0.0.1:3000 --token xxxx --user-id 1 \
      --key sk-4334... --proxy socks5h://mihomo:7890

退出码：0 成功 / 已存在；非 0 失败。
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

# 海外网关默认参数（域名/key 来自 v2 凭证文档；key 可被 --key 覆盖）
DEFAULT_NAME = "UEE NewAPI (海外二级网关)"
DEFAULT_BASE_URL = "https://api.ueejavelin.org"
DEFAULT_KEY = "sk-4334d49842dab480c5322d3bec167a9ab2b8fd5d"  # tony-main
DEFAULT_PROXY = "socks5h://mihomo:7890"
DEFAULT_GROUP = "default"
# type=1 (OpenAI)：base_url 只填域名，new-api 自动拼 /v1/chat/completions。
# 不要用 type=8 (Custom)！Custom 会把 base_url 当成完整请求 URL 直接用，
# base_url 只填域名时会打到根路径返回 HTML，relay 报 bad_response_body。
CHANNEL_TYPE_OPENAI = 1  # constant.ChannelTypeOpenAI

# Claude（Channel 1）+ GPT（Channel 2）；含别名 opus/sonnet/haiku（海外侧解析）。
# DeepSeek 故意不放——按需求暂不接入，国内直连官方更优。
DEFAULT_MODELS = [
    "claude-opus-4-7",
    "claude-opus-4-6",
    "claude-sonnet-4-6",
    "claude-haiku-4-5-20251001",
    "claude-haiku-4-5",
    "opus",
    "sonnet",
    "haiku",
    "gpt-5.5",
    "gpt-5.4",
    "gpt-5.4-mini",
    "gpt-5.3-codex",
    "gpt-5.2",
]


def _request(method, base, path, token, user_id, payload=None, timeout=30):
    url = base.rstrip("/") + path
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Authorization", token)  # 系统访问令牌，不带 Bearer
    req.add_header("New-Api-User", str(user_id))
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read().decode("utf-8")
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        raise SystemExit(f"[FAIL] {method} {path} -> HTTP {e.code}: {body}")
    except urllib.error.URLError as e:
        raise SystemExit(f"[FAIL] {method} {path} -> 连接失败: {e.reason}")
    try:
        return json.loads(body)
    except json.JSONDecodeError:
        raise SystemExit(f"[FAIL] {method} {path} -> 非 JSON 响应: {body[:300]}")


def find_existing(base, token, user_id, name):
    """按名称查重，避免重复建渠道。返回匹配到的渠道 dict 或 None。"""
    resp = _request(
        "GET",
        base,
        "/api/channel/search?keyword=" + urllib.parse.quote(name),
        token,
        user_id,
    )
    if not resp.get("success"):
        # 查询失败不致命，继续尝试创建（让后端自己判重/报错）
        print(f"[WARN] 查重接口返回 success=false: {resp.get('message')}")
        return None
    for ch in resp.get("data", []) or []:
        if ch.get("name") == name:
            return ch
    return None


def main():
    p = argparse.ArgumentParser(description="一键创建海外二级网关渠道")
    p.add_argument("--base", default=os.getenv("NEWAPI_BASE", "http://127.0.0.1:3000"))
    p.add_argument("--token", default=os.getenv("NEWAPI_TOKEN"))
    p.add_argument("--user-id", default=os.getenv("NEWAPI_USER_ID"))
    p.add_argument("--name", default=DEFAULT_NAME)
    p.add_argument("--base-url", default=DEFAULT_BASE_URL)
    p.add_argument("--key", default=os.getenv("NEWAPI_OVERSEAS_KEY", DEFAULT_KEY))
    p.add_argument("--proxy", default=DEFAULT_PROXY)
    p.add_argument("--group", default=DEFAULT_GROUP)
    p.add_argument("--type", type=int, default=CHANNEL_TYPE_OPENAI)
    p.add_argument("--models", default=",".join(DEFAULT_MODELS),
                   help="逗号分隔的模型列表")
    p.add_argument("--force", action="store_true",
                   help="即使同名渠道已存在也强制再建一条")
    args = p.parse_args()

    if not args.token or not args.user_id:
        p.error("必须提供 --token / NEWAPI_TOKEN 和 --user-id / NEWAPI_USER_ID")

    print(f"[*] 目标 new-api: {args.base}")
    existing = find_existing(args.base, args.token, args.user_id, args.name)
    if existing and not args.force:
        print(f"[=] 同名渠道已存在 (id={existing.get('id')})，跳过创建。"
              f"如需重建用 --force。")
        return 0

    setting = {"proxy": args.proxy} if args.proxy else {}
    channel = {
        "name": args.name,
        "type": args.type,
        "key": args.key,
        "base_url": args.base_url,
        "models": args.models,
        "group": args.group,
        "status": 1,
        # setting 是 JSON 字符串字段（dto.ChannelSettings），proxy 存这里
        "setting": json.dumps(setting),
    }
    payload = {"mode": "single", "channel": channel}

    resp = _request("POST", args.base, "/api/channel/", args.token, args.user_id, payload)
    if not resp.get("success"):
        raise SystemExit(f"[FAIL] 创建渠道失败: {resp.get('message')}")

    print("[OK] 渠道创建成功。")
    print(f"     名称: {args.name}")
    print(f"     类型: {args.type} (OpenAI)  Base URL: {args.base_url}")
    print(f"     代理: {args.proxy or '(无)'}")
    print(f"     分组: {args.group}")
    print(f"     模型: {args.models}")
    print("[i] 下一步：在后台对该渠道点「测试」，或直接发一条 claude-opus-4-7 请求验证。")
    return 0


if __name__ == "__main__":
    sys.exit(main())
