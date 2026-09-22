#!/usr/bin/env python3
"""Check installed native dependencies and sidecars without opening the desktop UI."""

import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import selectors
import subprocess
import urllib.parse
import urllib.request


EXECUTABLES = (
    "astrlink-desktop",
    "astrlink-core",
    "astrlink-mcp",
    "astrlink-privacy-worker",
    "astrlink-classifier-worker",
)
NOTICES = {
    "LICENSE.txt": "2f07c72751aed99790b8a4869cf2311df85a860b22ded05fa22803587a48922c",
    "ThirdPartyNotices.txt": "e9e90971a8e75a9a8ac0c6412e29c1202d079998389915aa485f46c816c3b4cc",
}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def command(args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=30)


def one(paths, label):
    matches = list(paths)
    require(len(matches) == 1, f"Expected exactly one {label}, found {matches}")
    return matches[0]


def check_binary(path, system, arch, logs):
    require(path.is_file() and os.access(path, os.X_OK), f"Missing executable: {path}")
    if system == "macos":
        architectures = command(["lipo", "-archs", str(path)]).split()
        require(architectures == [arch], f"Wrong architecture for {path}: {architectures}")
        return

    header = command(["readelf", "-h", str(path)])
    require("Advanced Micro Devices X86-64" in header, f"Not an x86_64 ELF: {path}")
    versions = command(["readelf", "--version-info", str(path)])
    for namespace, limit in (("GLIBC", (2, 36)), ("GLIBCXX", (3, 4, 30))):
        required = [tuple(map(int, v.split("."))) for v in re.findall(
            rf"\b{namespace}_([0-9]+(?:\.[0-9]+)+)", versions
        )]
        require(not required or max(required) <= limit,
                f"{path} requires {namespace} newer than Debian 12 supports")
    # Go binaries may be statically linked. Dynamic ELF binaries must resolve every library.
    dynamic = command(["readelf", "-d", str(path)])
    if "(NEEDED)" in dynamic:
        libraries = command(["ldd", str(path)])
        (logs / f"{path.name}.ldd.log").write_text(libraries)
        require("not found" not in libraries, f"Unresolved shared library in {path}")
    if path.name in ("astrlink-privacy-worker", "astrlink-classifier-worker"):
        require("libonnxruntime" not in dynamic, f"{path} must load ONNX Runtime dynamically")


def smoke_sidecars(executables, runtime, logs):
    env = dict(os.environ, ASTRLINK_CI_NO_REMOTE_MODELS="1",
               ASTRLINK_CI_SYNTHETIC_MODELS_ONLY="1", ASTRLINK_ONNX_RUNTIME_PATH=str(runtime))
    for name in ("astrlink-privacy-worker", "astrlink-classifier-worker", "astrlink-mcp"):
        path = executables[name]
        args = [str(path)] + (["--help"] if name == "astrlink-mcp" else [])
        with (logs / f"{name}.stdout.log").open("wb") as stdout, (
            logs / f"{name}.stderr.log"
        ).open("wb") as stderr:
            result = subprocess.run(args, stdin=subprocess.DEVNULL, stdout=stdout,
                                    stderr=stderr, env=env, timeout=15)
        error = (logs / f"{name}.stderr.log").read_text()
        if name == "astrlink-mcp":
            require(result.returncode == 0 and "Usage of" in error, "MCP sidecar failed to start")
        else:
            # This tests process startup and argument validation, not model inference.
            require(result.returncode == 1 and "startup_or_protocol_failure" in error,
                    f"{name} did not reach argument validation")

    core = executables["astrlink-core"]
    with (logs / "astrlink-core.stderr.log").open("wb") as stderr:
        process = subprocess.Popen(
            [str(core), "--inference-listen", "127.0.0.1:0", "--control-listen", "127.0.0.1:0"],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=stderr, env=env,
        )
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                require(selector.select(timeout=30), "Packaged Core did not report ready within 30 seconds")
                ready_line = process.stdout.readline()
            (logs / "astrlink-core.stdout.log").write_bytes(ready_line)
            ready = json.loads(ready_line)
            require(ready.get("event") == "ready", "Invalid Core startup event")
            control = ready["control_url"]
            parsed = urllib.parse.urlsplit(control)
            require(parsed.scheme == "http" and parsed.hostname == "127.0.0.1" and parsed.port,
                    "Core advertised a non-loopback control URL")
            client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            for endpoint in ("health", "version", "capabilities"):
                with client.open(f"{control}/control/v1/{endpoint}", timeout=10) as response:
                    payload = json.load(response)
                if endpoint == "health":
                    require(payload.get("status") == "ok", "Packaged Core health check failed")
                (logs / f"{endpoint}.json").write_text(json.dumps(payload, indent=2) + "\n")
            process.terminate()
            require(process.wait(timeout=10) == 0, "Core did not shut down cleanly")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=10)
            process.stdout.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=("macos", "linux"), required=True)
    parser.add_argument("--arch", choices=("arm64", "x86_64"), required=True)
    parser.add_argument("--root", type=Path, required=True, help="macOS .app or installed Linux filesystem root")
    parser.add_argument("--logs", type=Path, required=True)
    args = parser.parse_args()
    root = args.root.resolve()
    args.logs.mkdir(parents=True, exist_ok=True)
    require(platform.system() == ("Darwin" if args.platform == "macos" else "Linux"),
            "Verification must run on the target operating system")
    require(platform.machine() == args.arch, "Verification must run on the target architecture")

    if args.platform == "macos":
        bin_dir = root / "Contents/MacOS"
        runtime = root / "Contents/Frameworks/libonnxruntime.1.23.2.dylib"
        notices = root / "Contents/Resources/notices/onnxruntime-1.23.2"
    else:
        bin_dir = root / "usr/bin"
        lib_dir = root / "usr/lib"
        runtime = one(lib_dir.glob("*/onnxruntime/libonnxruntime.so.1.23.2"), "ONNX Runtime")
        notices = one(lib_dir.glob("*/notices/onnxruntime-1.23.2"), "ONNX Runtime notices")

    executables = {name: bin_dir / name for name in EXECUTABLES}
    for path in [*executables.values(), runtime]:
        check_binary(path, args.platform, args.arch, args.logs)
    for name, digest in NOTICES.items():
        require(hashlib.sha256((notices / name).read_bytes()).hexdigest() == digest,
                f"Packaged ONNX Runtime {name} does not match the pinned notice")

    repository = Path(__file__).resolve().parents[3]
    licenses = notices.parent.parent / "licenses"
    for name in ("LICENSE", "LICENSING.md", "LICENSES/AGPL-3.0.txt"):
        require((licenses / name).read_bytes() == (repository / name).read_bytes(),
                f"Packaged {name} does not match the repository license file")

    # Load the packaged runtime itself; worker argument validation alone does not load it.
    class ApiBase(ctypes.Structure):
        _fields_ = [("get_api", ctypes.c_void_p),
                    ("get_version", ctypes.CFUNCTYPE(ctypes.c_char_p))]

    library = ctypes.CDLL(str(runtime))
    library.OrtGetApiBase.restype = ctypes.POINTER(ApiBase)
    version = library.OrtGetApiBase().contents.get_version().decode()
    require(version == "1.23.2", f"Unexpected packaged ONNX Runtime: {version}")
    (args.logs / "onnxruntime-version.txt").write_text(version + "\n")
    smoke_sidecars(executables, runtime, args.logs)
    print(f"Verified {args.platform} {args.arch}: binaries, notices, runtime, sidecars and Core health")


if __name__ == "__main__":
    main()
