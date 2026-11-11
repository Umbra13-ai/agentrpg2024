"""Reads the godoc comment blocks of the handlers in cmd/server/*.go (@Summary, @Description, @Tags, @Param, @Router)
so the API reference can list every endpoint the code documents. Used by tools/build_swagger.py.

Run alone to print what it finds:  python tools/godoc_routes.py [filter]
"""
import glob
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TYPES = {"int": "integer", "integer": "integer", "string": "string", "bool": "boolean", "boolean": "boolean",
         "number": "number", "float": "number", "object": "object", "array": "array", "[]int": "array", "[]string": "array"}


def _schema_type(t):
    t = t.strip()
    if t.startswith("[]"):
        return {"type": "array", "items": {"type": TYPES.get(t[2:], "string")}}
    return {"type": TYPES.get(t, "string")}


def _body_schema(spec):
    """object{a=int,b=string} -> an OpenAPI object schema"""
    m = re.match(r"object\{(.*)\}$", spec.strip())
    if not m:
        return {"type": "object"}
    props = {}
    for part in re.split(r",(?![^{]*\})", m.group(1)):
        if "=" in part:
            k, v = part.split("=", 1)
            props[k.strip()] = _schema_type(v)
    return {"type": "object", "properties": props}


def routes():
    """Yields dicts: method, path (with /api), summary, description, tags, params (query/path), body (schema, description)."""
    for f in sorted(glob.glob(os.path.join(ROOT, "cmd", "server", "*.go"))):
        if f.endswith("_test.go"):
            continue
        block = []
        for line in open(f, encoding="utf-8"):
            s = line.strip()
            if s.startswith("//"):
                block.append(s[2:].strip())
                continue
            if block and any(b.startswith("@Router") for b in block):
                yield _parse(block)
            block = []


def _parse(block):
    r = {"summary": "", "description": "", "tags": [], "params": [], "body": None, "routes": []}
    for b in block:
        if b.startswith("@Summary"):
            r["summary"] = b[len("@Summary"):].strip()
        elif b.startswith("@Description"):
            r["description"] = (r["description"] + " " + b[len("@Description"):].strip()).strip()
        elif b.startswith("@Tags"):
            r["tags"] = [t.strip() for t in b[len("@Tags"):].split(",") if t.strip()]
        elif b.startswith("@Param"):
            m = re.match(r'@Param\s+(\S+)\s+(\S+)\s+(\S+)\s+(true|false)\s*"?(.*?)"?\s*$', b)
            if not m:
                continue
            name, where, typ, req, desc = m.groups()
            if where == "body":
                r["body"] = {"schema": _body_schema(typ), "description": desc, "required": req == "true"}
            elif where in ("query", "path"):
                r["params"].append({"name": name, "in": where, "required": req == "true" or where == "path",
                                    "description": desc, "schema": _schema_type(typ)})
        elif b.startswith("@Router"):
            m = re.match(r"@Router\s+(\S+)\s+\[(\w+)\]", b)
            if m:
                path = m.group(1)
                if not path.startswith("/api"):
                    path = "/api" + path
                r["routes"].append((m.group(2).lower(), path))
    return r


if __name__ == "__main__":
    flt = sys.argv[1] if len(sys.argv) > 1 else ""
    for r in routes():
        for method, path in r["routes"]:
            if flt in path:
                print(method.upper(), path, "|", r["summary"], "|", r["description"][:300])
