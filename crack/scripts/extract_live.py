#!/usr/bin/env python3
"""Structurally-redacting extractor for a LIVE Claude Code session capture.

Unlike split.py (which keeps bodies verbatim and relies on sanitize.py's
literal/regex map), this script is for dumps of a *real working session* whose
request bodies contain private conversation content. It walks each JSON body and
keeps only the fingerprint-bearing STRUCTURE — keys, block types, cache_control,
versions, betas, env, metadata shape — replacing free-text prose, code, tool
descriptions, and identity values with `<redacted …>` placeholders.

Usage:
    python3 crack/scripts/extract_live.py /path/to/whistle-dump.json [outdir]

Default outdir = crack/cc2156/rows/. The source dump is NOT copied or committed.
"""
import json, base64, gzip, subprocess, sys, os, collections

HERE = os.path.dirname(os.path.abspath(__file__))
CRACK_ROOT = os.path.dirname(HERE)

MASK_KEYS = {"device_id", "account_uuid", "organization_uuid", "email",
             "session_id", "user_id", "event_id", "rh", "previous_message_id"}
MASK_HEADERS = {"authorization", "x-api-key", "cookie", "set-cookie",
                "x-claude-code-session-id", "x-client-request-id", "request-id"}
KEEP_TEXT_PREFIXES = ("x-anthropic-billing-header:",
                      "You are Claude Code, Anthropic's official CLI for Claude.")
TEXT_LIMIT = 80


def decompress(raw, enc):
    if "gzip" in enc:
        try: return gzip.decompress(raw)
        except Exception: return raw
    if "br" in enc:
        p = subprocess.run(["brotli", "-d", "-c"], input=raw, capture_output=True)
        return p.stdout if p.returncode == 0 else raw
    return raw


def body_bytes(rec):
    b64 = rec.get("base64") or ""
    if not b64: return None
    raw = base64.b64decode(b64)
    return decompress(raw, (rec.get("headers") or {}).get("content-encoding", ""))


def redact(o, key=None):
    """Recursively keep structure, redact prose + identity values."""
    if isinstance(o, dict):
        return {k: redact(v, k) for k, v in o.items()}
    if isinstance(o, list):
        # collapse long homogeneous lists (messages, tools, events) to a marker
        if len(o) > 6 and all(isinstance(x, (dict, str)) for x in o):
            head = [redact(x) for x in o[:2]]
            return head + [f"<… {len(o) - 3} more items redacted …>", redact(o[-1])]
        return [redact(x) for x in o]
    if isinstance(o, str):
        if key in MASK_KEYS:
            return f"<masked:{key}>"
        if any(o.startswith(p) for p in KEEP_TEXT_PREFIXES):
            return o  # fingerprint-bearing — keep verbatim
        if len(o) > TEXT_LIMIT:
            return f"<text:{len(o)} chars>"
        return o
    return o


def redact_user_id(body_obj):
    """metadata.user_id is a JSON STRING; redact its inner identity fields."""
    md = body_obj.get("metadata")
    if isinstance(md, dict) and isinstance(md.get("user_id"), str):
        try:
            inner = json.loads(md["user_id"])
            md["user_id"] = json.dumps({k: f"<masked:{k}>" for k in inner})
        except Exception:
            md["user_id"] = "<masked:user_id>"


def summarize_body(text, url):
    """Return a redacted JSON object plus light structural notes."""
    try:
        obj = json.loads(text)
    except Exception:
        # base64/binary or plain text (e.g. releases endpoint = bare version)
        return {"_raw": text[:64]} if text else None
    notes = {}
    if isinstance(obj, dict):
        if isinstance(obj.get("messages"), list):
            notes["message_count"] = len(obj["messages"])
            notes["roles"] = collections.Counter(
                m.get("role") for m in obj["messages"] if isinstance(m, dict))
            notes["roles"] = dict(notes["roles"])
        if isinstance(obj.get("system"), list):
            notes["system_cache_pattern"] = [
                ("scope" in (b.get("cache_control") or {})) if isinstance(b, dict)
                and b.get("cache_control") else None
                for b in obj["system"]]
        if isinstance(obj.get("tools"), list):
            notes["tool_names"] = [t.get("name") for t in obj["tools"]
                                   if isinstance(t, dict)]
        redact_user_id(obj)
    if isinstance(obj, list) and obj and isinstance(obj[0], dict) and "events" not in obj[0]:
        notes["array_len"] = len(obj)
    red = redact(obj)
    return {"body": red, "_notes": notes} if notes else {"body": red}


def event_histogram(text):
    try:
        obj = json.loads(text)
    except Exception:
        return None
    evs = obj.get("events") if isinstance(obj, dict) else None
    if not isinstance(evs, list):
        return None
    return dict(collections.Counter(
        e.get("event_data", {}).get("event_name") for e in evs))


# Which sessions to keep: one representative per endpoint class. picker returns
# a sort key (class order, -bytes) so we grab the largest example of each class.
CLASSES = [
    ("v1_messages",   lambda u: "/v1/messages?beta" in u),
    ("count_tokens",  lambda u: "count_tokens" in u),
    ("event_logging_startup", lambda u: "event_logging" in u),  # largest = fat batch
    ("event_logging_steady",  lambda u: "event_logging" in u),  # smallest = single event
    ("datadog",       lambda u: "datadoghq" in u),
    ("releases",      lambda u: "claude-code-releases/latest" in u),
]


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: extract_live.py <whistle-dump.json> [outdir]")
    src = json.load(open(sys.argv[1]))
    out_dir = sys.argv[2] if len(sys.argv) > 2 else os.path.join(CRACK_ROOT, "cc2156", "rows")
    os.makedirs(out_dir, exist_ok=True)
    sessions = src["data"]["data"]

    rows = []
    for k, v in sessions.items():
        url = v.get("url", "")
        rq, rs = v.get("req", {}), v.get("res", {})
        rqb = body_bytes(rq)
        rsb = body_bytes(rs)
        rows.append({
            "rowId": k, "startTime": v.get("startTime"), "url": url,
            "method": rq.get("method"), "status": rs.get("statusCode"),
            "reqHeaders": rq.get("headers", {}), "resHeaders": rs.get("headers", {}),
            "reqSize": rq.get("size"), "resSize": rs.get("size"),
            "_reqText": rqb.decode("utf-8", "replace") if rqb else None,
            "_resText": rsb.decode("utf-8", "replace") if rsb else None,
        })

    picked, manifest = {}, []
    for cls, pred in CLASSES:
        cands = [r for r in rows if pred(r["url"]) and r["_reqText"] is not None
                 or (pred(r["url"]) and cls == "releases")]
        cands = [r for r in rows if pred(r["url"])]
        if not cands:
            continue
        reverse = "steady" not in cls  # steady = smallest, else largest
        cand = sorted(cands, key=lambda r: r.get("reqSize") or 0, reverse=reverse)[0]
        if cand["rowId"] in picked and cls != "event_logging_steady":
            continue
        picked.setdefault(cand["rowId"], cls)

        def hdr(h): return {k: ("<masked>" if k.lower() in MASK_HEADERS else val)
                            for k, val in (h or {}).items()}
        rec = {
            "class": cls, "url": cand["url"], "method": cand["method"],
            "status": cand["status"], "reqSize": cand["reqSize"], "resSize": cand["resSize"],
            "reqHeaders": hdr(cand["reqHeaders"]), "resHeaders": hdr(cand["resHeaders"]),
        }
        if "event_logging" in cls:
            rec["event_histogram"] = event_histogram(cand["_reqText"])
        rec["reqBody"] = summarize_body(cand["_reqText"], cand["url"]) if cand["_reqText"] else None
        rec["resBody"] = summarize_body(cand["_resText"], cand["url"]) if cand["_resText"] else None
        idx = len(manifest) + 1
        fn = os.path.join(out_dir, f"{idx:02d}-{cls}.json")
        json.dump(rec, open(fn, "w"), indent=2, ensure_ascii=False)
        manifest.append({"idx": idx, "class": cls, "file": os.path.basename(fn),
                         "url": cand["url"], "status": cand["status"],
                         "reqSize": cand["reqSize"]})
        print(f"{idx:02d} {cls:24s} {cand['status']} req={cand['reqSize']}  {cand['url'][:60]}")
    json.dump(manifest, open(os.path.join(out_dir, "_manifest.json"), "w"),
              indent=2, ensure_ascii=False)


if __name__ == "__main__":
    main()
