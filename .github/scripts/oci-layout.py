#!/usr/bin/env python3
"""Reads the OCI layouts that `docker buildx build --output type=oci` writes, for the image builds of step 17.

  oci-layout.py check <layout.tar> --name N --tags T1,T2 --revision R --version V --created C [--platforms P1,P2]
      Prints one "ok: ..." or "FAIL: ..." line per check and exits 1 on any FAIL: the tags the layout carries, the
      platforms, the attestation manifests (provenance and SBOM) and the labels and annotations of the proof-of-concept
      marking (Q4), on the index and on each platform's manifest and image configuration.
  oci-layout.py marking --name N --revision R --version V --created C
      Prints the marking as key=value lines, for the --label and --annotation arguments of a build.
  oci-layout.py load <layout.tar> <tag> [--platform linux/amd64]
      Writes a docker-archive of one platform's image to stdout, for `docker load`: the daemon cannot load an OCI layout.

Needs only the Python standard library.
"""
import argparse
import io
import json
import sys
import tarfile

TITLE = "org.opencontainers.image.title"
DESCRIPTION = "org.opencontainers.image.description"
SOURCE = "org.opencontainers.image.source"
REVISION = "org.opencontainers.image.revision"
VERSION = "org.opencontainers.image.version"
CREATED = "org.opencontainers.image.created"
LICENSES = "org.opencontainers.image.licenses"
SOURCE_URL = "https://github.com/open-feature-forking/openfeature-relay"
DESCRIPTION_TEXT = ("Proof of concept from the OpenFeature Relay incubator, built from main. "
                    "Not an official OpenFeature image; not for production.")


class Layout:
    def __init__(self, path):
        self.tar = tarfile.open(path)

    def raw(self, name):
        return self.tar.extractfile(name).read()

    def blob(self, digest):
        return json.loads(self.raw("blobs/sha256/" + digest.split(":", 1)[1]))

    def refs(self):
        """tag -> descriptor of the top-level index for every ref the layout carries."""
        found = {}
        for m in json.loads(self.raw("index.json"))["manifests"]:
            name = m.get("annotations", {}).get("io.containerd.image.name", "")
            found[name] = m
        return found

    def index(self, ref):
        return self.blob(self.refs()[ref]["digest"])

    def platform_manifests(self, index):
        return [m for m in index["manifests"] if m.get("platform", {}).get("os", "unknown") != "unknown"]

    def attestations(self, index):
        return [m for m in index["manifests"]
                if m.get("annotations", {}).get("vnd.docker.reference.type") == "attestation-manifest"]


def marking(name, revision, version):
    """The labels and annotations every published image carries (Q4)."""
    return {TITLE: name, DESCRIPTION: DESCRIPTION_TEXT, SOURCE: SOURCE_URL, REVISION: revision,
            VERSION: version, LICENSES: "Apache-2.0"}


def check(args):
    failures = 0

    def result(what, want, got):
        nonlocal failures
        if want == got:
            print(f"ok: {what}")
        else:
            failures += 1
            print(f"FAIL: {what}: expected {want!r}, got {got!r}")

    layout = Layout(args.layout)
    tags = sorted(args.tags.split(","))
    result("tags of the layout", tags, sorted(layout.refs()))
    ref = tags[0]
    index = layout.index(ref)
    platforms = sorted(m["platform"]["os"] + "/" + m["platform"]["architecture"] for m in layout.platform_manifests(index))
    result("platforms", sorted(args.platforms.split(",")), platforms)

    attestations = layout.attestations(index)
    result("attestation manifests", len(platforms), len(attestations))
    kinds = set()
    for a in attestations:
        for layer in layout.blob(a["digest"]).get("layers", []):
            kinds.add(layer.get("annotations", {}).get("in-toto.io/predicate-type", ""))
    result("provenance", True, any("slsa.dev/provenance" in k for k in kinds))
    result("SBOM", True, any("spdx" in k for k in kinds))

    want = marking(args.name, args.revision, args.version)
    index_annotations = index.get("annotations", {})
    result("index annotations", want, {k: index_annotations.get(k) for k in want})
    result("index created", args.created, index_annotations.get(CREATED))
    for m in layout.platform_manifests(index):
        arch = m["platform"]["architecture"]
        manifest = layout.blob(m["digest"])
        annotations = manifest.get("annotations", {})
        result(f"{arch}: manifest annotations", want, {k: annotations.get(k) for k in want})
        labels = layout.blob(manifest["config"]["digest"]).get("config", {}).get("Labels", {})
        result(f"{arch}: image labels", {**want, CREATED: args.created}, {k: labels.get(k) for k in {**want, CREATED: 0}})
    return 1 if failures else 0


def load(args):
    layout = Layout(args.layout)
    os_, arch = args.platform.split("/")
    index = layout.index(args.tag)
    chosen = [m for m in layout.platform_manifests(index)
              if m["platform"]["os"] == os_ and m["platform"]["architecture"] == arch]
    if len(chosen) != 1:
        sys.exit(f"the layout has no single {args.platform} image")
    manifest = layout.blob(chosen[0]["digest"])
    path = lambda d: "blobs/sha256/" + d.split(":", 1)[1]
    layers = [path(layer["digest"]) for layer in manifest["layers"]]
    config = path(manifest["config"]["digest"])
    out = tarfile.open(fileobj=sys.stdout.buffer, mode="w|")
    for name in [config, *layers]:
        info = layout.tar.getmember(name)
        out.addfile(info, layout.tar.extractfile(name))
    body = json.dumps([{"Config": config, "RepoTags": [args.tag], "Layers": layers}]).encode()
    info = tarfile.TarInfo("manifest.json")
    info.size = len(body)
    out.addfile(info, io.BytesIO(body))
    out.close()
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    c = sub.add_parser("check")
    c.add_argument("layout")
    c.add_argument("--name", required=True)
    c.add_argument("--tags", required=True)
    c.add_argument("--revision", required=True)
    c.add_argument("--version", required=True)
    c.add_argument("--created", required=True)
    c.add_argument("--platforms", default="linux/amd64,linux/arm64")
    k = sub.add_parser("marking")
    k.add_argument("--name", required=True)
    k.add_argument("--revision", required=True)
    k.add_argument("--version", required=True)
    k.add_argument("--created", required=True)
    l = sub.add_parser("load")
    l.add_argument("layout")
    l.add_argument("tag")
    l.add_argument("--platform", default="linux/amd64")
    args = parser.parse_args()
    if args.command == "marking":
        for key, value in {**marking(args.name, args.revision, args.version), CREATED: args.created}.items():
            print(f"{key}={value}")
        sys.exit(0)
    sys.exit(check(args) if args.command == "check" else load(args))


if __name__ == "__main__":
    main()
