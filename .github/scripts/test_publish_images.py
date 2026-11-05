"""The rehearsal of publish-images.py (step 17): it pushes synthetic OCI layouts to an in-process registry. Nothing here
talks to ghcr.io, and no test needs Docker; a rehearsal against a real registry:2 is in the PR text."""
import argparse
import hashlib
import http.server
import importlib.util
import io
import json
import os
import pathlib
import subprocess
import tarfile
import tempfile
import threading
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("publish_images", HERE / "publish-images.py")
publish = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publish)

INDEX = "application/vnd.oci.image.index.v1+json"
MANIFEST = "application/vnd.oci.image.manifest.v1+json"
FULL_OLD = "a" * 40
FULL_NEW = "b" * 40


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


class FakeRegistry(http.server.BaseHTTPRequestHandler):
    """The part of the distribution API that publish-images.py uses."""

    blobs, manifests, tags, log = {}, {}, {}, []

    def log_message(self, *args):
        pass

    def _send(self, code, headers=None, body=b""):
        self.send_response(code)
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def do_HEAD(self):
        self._route()

    def do_GET(self):
        self._route()

    def do_POST(self):
        parts = self.path.split("/")
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        self._send(202, {"Location": "/".join(parts[:-2]) + "/blobs/uploads/1"})

    def do_PUT(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        path, _, query = self.path.partition("?")
        repo = path.split("/v2/", 1)[1].split("/blobs/")[0].split("/manifests/")[0]
        if "/blobs/uploads/" in path:
            want = query.split("digest=", 1)[1].replace("%3A", ":")
            if digest(body) != want:
                return self._send(400)
            self.blobs[(repo, want)] = body
            return self._send(201)
        reference = path.rsplit("/manifests/", 1)[1]
        got = digest(body)
        if reference.startswith("sha256:") and reference != got:
            return self._send(400)
        self.manifests[(repo, got)] = (self.headers["Content-Type"], body)
        if not reference.startswith("sha256:"):
            self.tags[(repo, reference)] = got
        self.log.append((repo, reference))
        self._send(201, {"Docker-Content-Digest": got})

    def _route(self):
        path = self.path
        repo = path.split("/v2/", 1)[1].split("/blobs/")[0].split("/manifests/")[0]
        if "/blobs/" in path:
            found = (repo, path.rsplit("/", 1)[1]) in self.blobs
            return self._send(200 if found else 404)
        reference = path.rsplit("/manifests/", 1)[1]
        key = reference if reference.startswith("sha256:") else self.tags.get((repo, reference))
        if (repo, key) not in self.manifests:
            return self._send(404)
        media, body = self.manifests[(repo, key)]
        self._send(200, {"Content-Type": media, "Docker-Content-Digest": key}, body)


def add(tar, name, data):
    info = tarfile.TarInfo(name)
    info.size = len(data)
    tar.addfile(info, io.BytesIO(data))


def write_layout(path, refs, revision):
    """A layout with an amd64 and an arm64 image and one attestation manifest, under the given refs."""
    blobs = {}

    def blob(data):
        blobs["blobs/sha256/" + digest(data).split(":", 1)[1]] = data
        return digest(data)

    children = []
    for arch in ("amd64", "arm64", None):
        config_data, layer_data = json.dumps({"arch": arch, "n": str(path)}).encode(), f"layer {arch} {path}".encode()
        config, layer = blob(config_data), blob(layer_data)
        manifest = json.dumps({"schemaVersion": 2, "mediaType": MANIFEST,
                               "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": config, "size": len(config_data)},
                               "layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": layer, "size": len(layer_data)}]}).encode()
        child = {"mediaType": MANIFEST, "digest": blob(manifest), "size": len(manifest)}
        if arch:
            child["platform"] = {"os": "linux", "architecture": arch}
        else:
            child["platform"] = {"os": "unknown", "architecture": "unknown"}
            child["annotations"] = {"vnd.docker.reference.type": "attestation-manifest"}
        children.append(child)
    index = json.dumps({"schemaVersion": 2, "mediaType": INDEX, "manifests": children,
                        "annotations": {publish.REVISION: revision}}).encode()
    top = blob(index)
    with tarfile.open(path, "w") as tar:
        add(tar, "oci-layout", b'{"imageLayoutVersion":"1.0.0"}')
        add(tar, "index.json", json.dumps({"schemaVersion": 2, "manifests": [
            {"mediaType": INDEX, "digest": top, "size": len(index), "annotations": {"io.containerd.image.name": ref}}
            for ref in refs]}).encode())
        for name, data in blobs.items():
            add(tar, name, data)
    return top


def refs_for(name, short):
    return [f"ghcr.io/open-feature-forking/{name}:main", f"ghcr.io/open-feature-forking/{name}:sha-{short}"]


class PublishImages(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        FakeRegistry.blobs, FakeRegistry.manifests, FakeRegistry.tags, FakeRegistry.log = {}, {}, {}, []
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), FakeRegistry)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        os.environ["REGISTRY_HOST"] = f"127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        os.environ.pop("REGISTRY_HOST")

    def setUp(self):
        FakeRegistry.blobs.clear()
        FakeRegistry.manifests.clear()
        FakeRegistry.tags.clear()
        FakeRegistry.log.clear()
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.messages = []

    def layouts(self, short, revision, names=publish.NAMES):
        return {name: write_layout(pathlib.Path(self.dir.name) / f"{name}.tar", refs_for(name, short), revision)
                for name in names}

    def push(self, short="abc1234", **kwargs):
        args = argparse.Namespace(layouts=self.dir.name, short=short, names=kwargs.get("names"), outputs=kwargs.get("outputs"))
        return publish.push(args, self.messages.append)

    def test_pushes_by_digest_and_under_the_sha_tag_and_never_under_main(self):
        digests = self.layouts("abc1234", FULL_OLD)
        self.push()
        for name, want in digests.items():
            repo = f"open-feature-forking/{name}"
            self.assertEqual(want, FakeRegistry.tags[(repo, "sha-abc1234")], name)
            # the index, both platform manifests and the attestation manifest are stored by digest
            index = json.loads(FakeRegistry.manifests[(repo, want)][1])
            self.assertEqual(3, len(index["manifests"]))
            for child in index["manifests"]:
                self.assertIn((repo, child["digest"]), FakeRegistry.manifests)
        self.assertEqual([], [t for t in FakeRegistry.tags if t[1].startswith("main")])
        self.assertEqual(7, len([m for m in self.messages if m.startswith("- pushed ")]))

    def test_outputs_carry_each_pushed_digest(self):
        digests = self.layouts("abc1234", FULL_OLD)
        out = pathlib.Path(self.dir.name) / "outputs"
        self.push(outputs=str(out))
        lines = dict(line.split("=") for line in out.read_text().split())
        self.assertEqual({k.replace("-", "_"): v for k, v in digests.items()}, lines)

    def test_a_destination_outside_the_seven_is_refused_before_anything_is_pushed(self):
        self.layouts("abc1234", FULL_OLD)
        write_layout(pathlib.Path(self.dir.name) / "flagd-sync.tar",
                     ["ghcr.io/open-feature/flagd-sync:latest", "ghcr.io/open-feature/flagd:v1.2.3"], FULL_OLD)
        with self.assertRaises(publish.Refused):
            self.push()
        self.assertEqual([], FakeRegistry.log)
        self.assertEqual({}, FakeRegistry.blobs)

    def test_a_layout_cannot_publish_into_another_images_repository(self):
        self.layouts("abc1234", FULL_OLD)
        write_layout(pathlib.Path(self.dir.name) / "flagd-sync.tar", refs_for("flagd-cloud", "abc1234"), FULL_OLD)
        with self.assertRaises(publish.Refused):
            self.push()
        self.assertEqual([], FakeRegistry.log)

    def test_a_tag_other_than_main_or_sha_is_refused(self):
        self.layouts("abc1234", FULL_OLD)
        write_layout(pathlib.Path(self.dir.name) / "flagd-sync.tar",
                     ["ghcr.io/open-feature-forking/flagd-sync:latest", "ghcr.io/open-feature-forking/flagd-sync:sha-abc1234"], FULL_OLD)
        with self.assertRaises(publish.Refused):
            self.push()

    def test_a_missing_testbed_layout_is_skipped_and_reported(self):
        self.layouts("abc1234", FULL_OLD)
        (pathlib.Path(self.dir.name) / "flagd-testbed.tar").unlink()
        self.push()
        self.assertIn("- skipped flagd-testbed: no layout was built in this run", self.messages)
        self.assertEqual(6, len([m for m in self.messages if m.startswith("- pushed ")]))

    def test_a_missing_distribution_layout_is_an_error(self):
        self.layouts("abc1234", FULL_OLD)
        (pathlib.Path(self.dir.name) / "flagd-sync.tar").unlink()
        with self.assertRaises(publish.Refused):
            self.push()
        self.assertEqual([], FakeRegistry.log)

    def test_only_ghcr_or_a_local_registry_is_a_host(self):
        os.environ["REGISTRY_HOST"] = "registry.example.com"
        self.addCleanup(os.environ.__setitem__, "REGISTRY_HOST", f"127.0.0.1:{self.server.server_port}")
        self.layouts("abc1234", FULL_OLD)
        with self.assertRaises(publish.Refused):
            self.push()

    def test_main_moves_forward_and_an_older_commit_published_later_leaves_it_alone(self):
        repo = pathlib.Path(self.dir.name) / "repo"
        repo.mkdir()
        git = lambda *a: subprocess.run(["git", "-C", str(repo), "-c", "user.email=a@b", "-c", "user.name=x", *a],
                                        capture_output=True, text=True, check=True).stdout.strip()
        git("init", "-q")
        git("commit", "-q", "--allow-empty", "-m", "old")
        old = git("rev-parse", "HEAD")
        git("commit", "-q", "--allow-empty", "-m", "new")
        new = git("rev-parse", "HEAD")
        names = ["flagd-sync"]
        for sha in (new, old):
            shutil_dir = pathlib.Path(self.dir.name) / f"layouts-{sha[:7]}"
            shutil_dir.mkdir()
            write_layout(shutil_dir / "flagd-sync.tar", refs_for("flagd-sync", sha[:7]), sha)
            publish.push(argparse.Namespace(layouts=str(shutil_dir), short=sha[:7], names="flagd-sync", outputs=None), lambda m: None)
        cwd = os.getcwd()
        os.chdir(repo)
        self.addCleanup(os.chdir, cwd)
        move = lambda sha: publish.move_main(argparse.Namespace(short=sha[:7], sha=sha, names=",".join(names)), self.messages.append)
        move(old)  # no :main yet
        self.assertEqual(FakeRegistry.tags[("open-feature-forking/flagd-sync", f"sha-{old[:7]}")],
                         FakeRegistry.tags[("open-feature-forking/flagd-sync", "main")])
        move(new)  # a descendant
        self.assertEqual(FakeRegistry.tags[("open-feature-forking/flagd-sync", f"sha-{new[:7]}")],
                         FakeRegistry.tags[("open-feature-forking/flagd-sync", "main")])
        self.messages.clear()
        move(old)  # an older commit published after a newer one
        self.assertEqual(FakeRegistry.tags[("open-feature-forking/flagd-sync", f"sha-{new[:7]}")],
                         FakeRegistry.tags[("open-feature-forking/flagd-sync", "main")])
        self.assertTrue(any(m.startswith("- kept open-feature-forking/flagd-sync:main") for m in self.messages), self.messages)

    def test_no_fips_tag_is_a_destination(self):
        self.layouts("abc1234", FULL_OLD)
        write_layout(pathlib.Path(self.dir.name) / "flagd-sync.tar",
                     ["ghcr.io/open-feature-forking/flagd-sync:main-fips", "ghcr.io/open-feature-forking/flagd-sync:sha-abc1234-fips"], FULL_OLD)
        with self.assertRaises(publish.Refused):
            self.push()
        self.assertEqual([], FakeRegistry.log)


if __name__ == "__main__":
    unittest.main()
