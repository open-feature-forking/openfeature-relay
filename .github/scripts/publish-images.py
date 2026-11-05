#!/usr/bin/env python3
"""Pushes the OCI layouts that image-build.sh, image-testbed.sh and the distributions part built (step 17, #216).

  publish-images.py push <layouts directory> --short <7 hex> [--names n1,n2] [--outputs <file>]
      Pushes every layout by digest and under its sha-<short> tags. It never pushes a main tag: move-main does.
  publish-images.py move-main --short <7 hex> --sha <full commit> [--names n1,n2]
      Moves the main tags to the sha-<short> images, only if the commit :main points at is an ancestor of --sha.

There is one image per name: no FIPS variant is published.

The destinations are the seven names below under ghcr.io/open-feature-forking/ and nothing else. A layout names two refs
under ghcr.io/open-feature/..., which are never read as destinations. Every layout is checked against the allowlist before
the first byte is pushed. The registry host is ghcr.io; REGISTRY_HOST may name a local registry (localhost or 127.0.0.1,
plain http) for the rehearsal. Credentials: REGISTRY_USER and REGISTRY_TOKEN, as the user and token of a Basic login.

Needs only the Python standard library.
"""
import argparse
import base64
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

NAMESPACE = "open-feature-forking"
DISTRIBUTIONS = ("flagd-full", "flagd-minimal", "flagd-minimal-otel", "flagd-cloud", "flagd-kubernetes", "flagd-sync")
TESTBED = "flagd-testbed"
NAMES = DISTRIBUTIONS + (TESTBED,)
TAG = re.compile(r"^(main|sha-[0-9a-f]{7})$")
REVISION = "org.opencontainers.image.revision"
INDEX_TYPES = ("application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json")
ACCEPT = ", ".join(INDEX_TYPES + ("application/vnd.oci.image.manifest.v1+json",
                                  "application/vnd.docker.distribution.manifest.v2+json"))

_spec = importlib.util.spec_from_file_location("oci_layout", pathlib.Path(__file__).with_name("oci-layout.py"))
oci_layout = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(oci_layout)


class Refused(Exception):
    """A layout asks for a destination that is not one of the seven images."""


def layout_files(directory, names):
    """name -> path of the layouts to push, and the optional ones that are missing."""
    found, missing = {}, []
    for name in names:
        path = pathlib.Path(directory) / f"{name}.tar"
        if path.exists():
            found[name] = path
        elif name == TESTBED:
            missing.append(name)
        else:
            raise Refused(f"the layout of {name} is missing: {path}")
    return found, missing


def check_destinations(key, layout, short):
    """Every ref the layout carries must be the image's own repository under the allowlist, with an allowed tag."""
    name = key
    if name not in NAMES:
        raise Refused(f"{key} is not one of the images to publish")
    want = {f"ghcr.io/{NAMESPACE}/{name}:main", f"ghcr.io/{NAMESPACE}/{name}:sha-{short}"}
    refs = set(layout.refs())
    for ref in refs:
        repository, _, tag = ref.rpartition(":")
        if repository != f"ghcr.io/{NAMESPACE}/{name}" or not TAG.match(tag):
            raise Refused(f"{key}: the layout names {ref}, which is not a destination of {name}")
    if refs != want:
        raise Refused(f"{key}: the layout carries {sorted(refs)}, not {sorted(want)}")


class Registry:
    def __init__(self, host, user="", token=""):
        self.host = host
        self.local = host.split(":")[0] in ("localhost", "127.0.0.1")
        self.base = ("http://" if self.local else "https://") + host
        self.basic = base64.b64encode(f"{user}:{token}".encode()).decode() if token else ""
        self.tokens = {}

    def open(self, method, url, headers=None, body=None, scope=None, length=None):
        headers = dict(headers or {})
        if scope in self.tokens:
            headers["Authorization"] = "Bearer " + self.tokens[scope]
        elif self.basic and self.local:
            headers["Authorization"] = "Basic " + self.basic
        if length is not None:
            headers["Content-Length"] = str(length)
        request = urllib.request.Request(url, data=body, method=method, headers=headers)
        try:
            return urllib.request.urlopen(request)
        except urllib.error.HTTPError as err:
            if err.code == 401 and scope and scope not in self.tokens and err.headers.get("WWW-Authenticate"):
                self.authenticate(err.headers["WWW-Authenticate"], scope)
                if hasattr(body, "seek"):
                    body.seek(0)
                return self.open(method, url, headers, body, scope, length)
            raise

    def authenticate(self, challenge, scope):
        params = dict(re.findall(r'(\w+)="([^"]*)"', challenge))
        query = urllib.parse.urlencode({"service": params.get("service", ""), "scope": scope})
        request = urllib.request.Request(f"{params['realm']}?{query}")
        if self.basic:
            request.add_header("Authorization", "Basic " + self.basic)
        with urllib.request.urlopen(request) as response:
            self.tokens[scope] = json.load(response).get("token") or ""

    def scope(self, repository):
        return f"repository:{repository}:push,pull"

    def blob_exists(self, repository, digest):
        try:
            self.open("HEAD", f"{self.base}/v2/{repository}/blobs/{digest}", scope=self.scope(repository)).close()
            return True
        except urllib.error.HTTPError as err:
            if err.code == 404:
                return False
            raise

    def push_blob(self, repository, digest, stream, size):
        if self.blob_exists(repository, digest):
            return
        scope = self.scope(repository)
        start = self.open("POST", f"{self.base}/v2/{repository}/blobs/uploads/", scope=scope, length=0)
        location = start.headers["Location"]
        start.close()
        if location.startswith("/"):
            location = self.base + location
        location += ("&" if "?" in location else "?") + urllib.parse.urlencode({"digest": digest})
        self.open("PUT", location, {"Content-Type": "application/octet-stream"}, stream, scope=scope, length=size).close()

    def push_manifest(self, repository, reference, media_type, body):
        response = self.open("PUT", f"{self.base}/v2/{repository}/manifests/{reference}", {"Content-Type": media_type}, body,
                             scope=self.scope(repository), length=len(body))
        digest = response.headers.get("Docker-Content-Digest", "")
        response.close()
        return digest

    def get_manifest(self, repository, reference):
        """(media type, raw bytes) or None when the reference does not exist."""
        try:
            with self.open("GET", f"{self.base}/v2/{repository}/manifests/{reference}", {"Accept": ACCEPT},
                           scope=self.scope(repository)) as response:
                return response.headers.get("Content-Type", "").split(";")[0], response.read()
        except urllib.error.HTTPError as err:
            if err.code == 404:
                return None
            raise


def push_layout(registry, key, layout, short, log):
    name = key
    repository = f"{NAMESPACE}/{name}"
    ref = f"ghcr.io/{repository}:sha-{short}"
    descriptor = layout.refs()[ref]
    index_digest = descriptor["digest"]
    index = layout.blob(index_digest)
    blob_name = lambda digest: "blobs/sha256/" + digest.split(":", 1)[1]

    def blob(digest, size):
        registry.push_blob(repository, digest, layout.tar.extractfile(blob_name(digest)), size)

    for child in index["manifests"]:
        manifest = layout.blob(child["digest"])
        blob(manifest["config"]["digest"], manifest["config"]["size"])
        for item in manifest.get("layers", []):
            blob(item["digest"], item["size"])
    # the platform and attestation manifests by digest, then the index by digest, then its tags
    for child in index["manifests"]:
        got = registry.push_manifest(repository, child["digest"], child["mediaType"], layout.raw(blob_name(child["digest"])))
        if got != child["digest"]:
            raise RuntimeError(f"{repository}: pushed {child['digest']}, the registry stored {got}")
    raw = layout.raw(blob_name(index_digest))
    got = registry.push_manifest(repository, index_digest, descriptor["mediaType"], raw)
    if got != index_digest:
        raise RuntimeError(f"{repository}: pushed {index_digest}, the registry stored {got}")
    got = registry.push_manifest(repository, f"sha-{short}", descriptor["mediaType"], raw)
    if got != index_digest:
        raise RuntimeError(f"{repository}: the tag sha-{short} holds {got}, not {index_digest}")
    log(f"- pushed {repository}:sha-{short} = {index_digest}")
    return index_digest


def registry_from_env():
    host = os.environ.get("REGISTRY_HOST", "ghcr.io")
    if host != "ghcr.io" and host.split(":")[0] not in ("localhost", "127.0.0.1"):
        raise Refused(f"REGISTRY_HOST {host} is neither ghcr.io nor a local registry")
    return Registry(host, os.environ.get("REGISTRY_USER", ""), os.environ.get("REGISTRY_TOKEN", ""))


def push(args, log=print):
    names = args.names.split(",") if args.names else NAMES
    for name in names:
        if name not in NAMES:
            raise Refused(f"{name} is not one of the images to publish")
    files, missing = layout_files(args.layouts, names)
    layouts = {key: oci_layout.Layout(path) for key, path in files.items()}
    for key, layout in layouts.items():
        check_destinations(key, layout, args.short)
    registry = registry_from_env()
    for name in missing:
        log(f"- skipped {name}: no layout was built in this run")
    outputs = {}
    for key, layout in layouts.items():
        outputs[key.replace("-", "_")] = push_layout(registry, key, layout, args.short, log)
    if args.outputs:
        with open(args.outputs, "a") as out:
            for key, digest in outputs.items():
                out.write(f"{key}={digest}\n")
    return 0


def is_ancestor(old, new):
    return subprocess.run(["git", "merge-base", "--is-ancestor", old, new], capture_output=True).returncode == 0


def move_main(args, log=print):
    names = args.names.split(",") if args.names else NAMES
    registry = registry_from_env()
    for name in names:
        if name not in NAMES:
            raise Refused(f"{name} is not one of the images to publish")
        repository = f"{NAMESPACE}/{name}"
        new = registry.get_manifest(repository, f"sha-{args.short}")
        if new is None:
            log(f"- kept {repository}:main: sha-{args.short} was not published")
            continue
        current = registry.get_manifest(repository, "main")
        if current is not None:
            revision = json.loads(current[1]).get("annotations", {}).get(REVISION, "")
            if revision == args.sha:
                log(f"- {repository}:main already holds {args.sha[:7]}")
                continue
            if not revision or not is_ancestor(revision, args.sha):
                log(f"- kept {repository}:main: it holds {revision[:7] or 'an unknown commit'}, which is not an ancestor of {args.sha[:7]}")
                continue
        registry.push_manifest(repository, "main", new[0], new[1])
        log(f"- moved {repository}:main to {args.sha[:7]}")
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("push")
    p.add_argument("layouts")
    p.add_argument("--short", required=True)
    p.add_argument("--names")
    p.add_argument("--outputs")
    m = sub.add_parser("move-main")
    m.add_argument("--short", required=True)
    m.add_argument("--sha", required=True)
    m.add_argument("--names")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{7}", args.short):
        sys.exit("--short is seven hex digits")
    try:
        sys.exit(push(args) if args.command == "push" else move_main(args))
    except Refused as err:
        print(f"::error::refused: {err}", file=sys.stderr)
        sys.exit(3)


if __name__ == "__main__":
    main()
