#!/usr/bin/env python3
"""Install or update the preview start workflow in Kibana, or run it once.

Environment:
  KIBANA_URL, ELASTIC_API_KEY     the Kibana project and an API key that can manage workflows
  DEMO_CALLBACK_SECRET            shared with the plugin's CallbackSecret setting (24+ characters)
  PREVIEW_DOMAIN                  optional, default test.mattermost.cloud
  CLEANUP_MINUTES                 optional, default 120: age after which demo cases and watch rules are deleted

Usage:
  python3 elastic/install_workflow.py install
  python3 elastic/install_workflow.py run --instance abc123 [--team meridian-soc] [--analyst priya.raman]
"""

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

WORKFLOW_ID = os.environ.get("WORKFLOW_ID", "insider-threat-preview-start")
YAML = Path(__file__).with_name("preview_start.workflow.yaml")


def kibana(method, path, body=None):
    req = urllib.request.Request(
        os.environ["KIBANA_URL"].rstrip("/") + path, method=method,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Authorization": f"ApiKey {os.environ['ELASTIC_API_KEY']}", "kbn-xsrf": "true",
                 "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            raw = resp.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        if e.code == 404 and method == "GET":
            return None
        sys.exit(f"{method} {path}: {e.code} {e.read().decode()[:500]}")


def install():
    secret = os.environ.get("DEMO_CALLBACK_SECRET", "")
    domain = os.environ.get("PREVIEW_DOMAIN", "test.mattermost.cloud")
    minutes = os.environ.get("CLEANUP_MINUTES", "120")
    if len(secret) < 24 or not re.fullmatch(r"[A-Za-z0-9_-]+", secret):
        sys.exit("DEMO_CALLBACK_SECRET must be 24+ letters, digits, - or _")
    if not re.fullmatch(r"[a-z0-9-]+(\.[a-z0-9-]+)+", domain):
        sys.exit(f"bad PREVIEW_DOMAIN {domain!r}")
    if not minutes.isdigit() or int(minutes) < 1:
        sys.exit(f"bad CLEANUP_MINUTES {minutes!r}")
    yaml = (YAML.read_text().replace("__CALLBACK_SECRET__", secret).replace("__PREVIEW_DOMAIN__", domain)
            .replace("__CLEANUP_MINUTES__", minutes))
    if kibana("GET", f"/api/workflows/workflow/{WORKFLOW_ID}"):
        kibana("PUT", f"/api/workflows/workflow/{WORKFLOW_ID}", {"yaml": yaml})
        print(f"workflow updated: {WORKFLOW_ID} (calls back to *.{domain})")
    else:
        kibana("POST", "/api/workflows", {"workflows": [{"id": WORKFLOW_ID, "yaml": yaml}]})
        print(f"workflow created: {WORKFLOW_ID} (calls back to *.{domain})")


def run(args):
    inputs = {"instance": args.instance, "team": args.team, "analyst": args.analyst}
    exec_id = kibana("POST", f"/api/workflows/workflow/{WORKFLOW_ID}/run", {"inputs": inputs})["workflowExecutionId"]
    for _ in range(60):
        time.sleep(2)
        ex = kibana("GET", f"/api/workflows/executions/{exec_id}")
        if ex and ex.get("status") not in ("pending", "running", "waiting"):
            for step in ex.get("stepExecutions", []):
                print(f"  {step.get('stepId')}: {step.get('status')}" + (f"  {step.get('error')}" if step.get("error") else ""))
            print(f"execution {exec_id}: {ex.get('status')}")
            return
    print(f"execution {exec_id} still running")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("install")
    r = sub.add_parser("run")
    r.add_argument("--instance", required=True)
    r.add_argument("--team", default="meridian-soc")
    r.add_argument("--analyst", default="priya.raman")
    args = ap.parse_args()
    install() if args.cmd == "install" else run(args)


if __name__ == "__main__":
    main()
