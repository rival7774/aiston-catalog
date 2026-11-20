"""Integration check against the running seeded catalog, using Python stdlib."""
import json
import os
from urllib.parse import urlencode
from urllib.request import urlopen

BASE = os.getenv("CATALOG_URL", "http://localhost:8080") + "/api/categories"


def get(path):
    with urlopen(BASE + path, timeout=5) as response:
        return json.load(response)


roots = get("")
assert sum(node["count"] for node in roots) == 200_000
# Walk a whole small subtree, crossing tied timestamps at page boundaries.
leaf = get("/200/path")[-1]
assert not leaf["hasChildren"]
ids = []
cursor = None
while True:
    params = {"limit": 7}
    if cursor:
        params["cursor"] = cursor
    page = get("/200/products?" + urlencode(params))
    ids.extend(item["id"] for item in page["items"])
    cursor = page["nextCursor"]
    if cursor is None:
        break
assert ids == list(range(200_000, 0, -200))
assert len(ids) == leaf["count"]
# Root pages contain many equal timestamps. Compare with the seed independently.
root = roots[0]["id"]
def belongs(category):
    while category > 5:
        category = (category - 6) // 4 + 1
    return category == root

expected = [i for i in range(200_000, 0, -1) if belongs((i - 1) % 200 + 1)]
assert len(expected) == roots[0]["count"]
first = get(f"/{root}/products?limit=7")
second = get(f"/{root}/products?" + urlencode({"limit": 7, "cursor": first["nextCursor"]}))
assert [p["id"] for p in first["items"] + second["items"]] == expected[:14]

