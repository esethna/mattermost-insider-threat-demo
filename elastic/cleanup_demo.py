#!/usr/bin/env python3
"""Delete what demos leave behind in Elastic: cases and watch rules tagged mattermost-demo, and any
case opened for the demo alert. Seed data and the seed's detection rules are untouched.

Environment: KIBANA_URL, ELASTIC_API_KEY (optional KIBANA_SPACE, default "default").
"""

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

DEMO_TAG = "mattermost-demo"
CASE_TITLE_PREFIX = "Large print job outside working hours"


def kibana(method, path, body=None):
    space = os.environ.get("KIBANA_SPACE", "default")
    base = os.environ["KIBANA_URL"].rstrip("/") + ("" if space == "default" else f"/s/{space}")
    req = urllib.request.Request(base + path, method=method,
                                 data=None if body is None else json.dumps(body).encode(),
                                 headers={"Authorization": f"ApiKey {os.environ['ELASTIC_API_KEY']}",
                                          "kbn-xsrf": "true", "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            raw = resp.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {path}: {e.code} {e.read().decode()[:300]}")


def demo_cases():
    ids, page = set(), 1
    while True:
        q = urllib.parse.urlencode({"owner": "securitySolution", "perPage": 100, "page": page})
        found = kibana("GET", f"/api/cases/_find?{q}")
        for c in found.get("cases", []):
            if DEMO_TAG in c.get("tags", []) or c.get("title", "").startswith(CASE_TITLE_PREFIX):
                ids.add(c["id"])
        if page * 100 >= found.get("total", 0):
            return sorted(ids)
        page += 1


def main():
    cases = demo_cases()
    for i in range(0, len(cases), 50):
        batch = urllib.parse.quote(json.dumps(cases[i:i + 50]))
        kibana("DELETE", f"/api/cases?ids={batch}")
    print(f"cases deleted: {len(cases)}")

    q = urllib.parse.urlencode({"filter": f'alert.attributes.tags:"{DEMO_TAG}"', "per_page": 100})
    rules = [r["id"] for r in kibana("GET", f"/api/detection_engine/rules/_find?{q}").get("data", [])]
    if rules:
        kibana("POST", "/api/detection_engine/rules/_bulk_action", {"action": "delete", "ids": rules})
    print(f"watch rules deleted: {len(rules)}")


if __name__ == "__main__":
    main()
