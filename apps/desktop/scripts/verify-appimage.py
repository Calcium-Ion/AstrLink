#!/usr/bin/env python3
"""Start the AppImage headless and check that it runs from the image.

The app has to run with APPIMAGE naming the file, which its updater
replaces, and the image's WebKitGTK has to start its own helper processes.
Run it where WebKitGTK is not installed, under Xvfb, after extracting the
image to --appdir (`<AppImage> --appimage-extract`).
"""

import argparse
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def check_libraries(appdir, logs):
    """Every library in the image resolves, from the image or the system."""
    env = dict(os.environ, LD_LIBRARY_PATH=str(appdir / "usr/lib"))
    missing = []
    report = []
    for path in sorted((appdir / "usr/lib").rglob("*")):
        if not path.is_file() or path.is_symlink() or (appdir / "usr/lib/astrlink") in path.parents:
            continue
        with path.open("rb") as file:
            if file.read(4) != b"\x7fELF":
                continue
        result = subprocess.run(["ldd", str(path)], capture_output=True, text=True, env=env, timeout=30)
        report.append(f"{path.relative_to(appdir)}\n{result.stdout}{result.stderr}")
        missing += [f"{path.relative_to(appdir)}: {line.strip()}"
                    for line in (result.stdout + result.stderr).splitlines() if "not found" in line]
    (logs / "appimage-ldd.log").write_text("\n".join(report))
    require(not missing, "Unresolved libraries in the AppImage:\n" + "\n".join(missing))


def processes():
    for entry in Path("/proc").iterdir():
        if entry.name.isdigit():
            try:
                yield int(entry.name), os.readlink(entry / "exe"), entry
            except OSError:
                continue


def environment(proc):
    data = (proc / "environ").read_bytes().decode(errors="replace")
    return dict(item.split("=", 1) for item in data.split("\0") if "=" in item)


def check_start(appimage, logs, timeout):
    """The app runs from the image, with its helper processes and Core."""
    home = Path(tempfile.mkdtemp(prefix="astrlink-appimage-home-"))
    runtime = home / "runtime"
    runtime.mkdir(mode=0o700)
    env = {
        "PATH": os.environ["PATH"],
        "HOME": str(home),
        "XDG_RUNTIME_DIR": str(runtime),
        "LANG": "C.UTF-8",
        # Containers have no FUSE: the runtime extracts the image instead
        # of mounting it, and sets APPIMAGE and APPDIR the same way.
        "APPIMAGE_EXTRACT_AND_RUN": "1",
        # Not ASTRLINK_CI_NO_REMOTE_MODELS: with it Core refuses to start
        # the way the app starts it. Starting downloads no model.
        "ASTRLINK_CI_SYNTHETIC_MODELS_ONLY": "1",
    }
    found = {}
    with (logs / "appimage.stdout.log").open("wb") as stdout, (
        logs / "appimage.stderr.log"
    ).open("wb") as stderr:
        process = subprocess.Popen(
            ["xvfb-run", "-a", "-s", "-screen 0 1280x720x24", str(appimage)],
            stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr, env=env,
            start_new_session=True,
        )
        try:
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline and len(found) < 4:
                require(process.poll() is None,
                        f"The AppImage exited with {process.returncode} (see appimage.stderr.log)")
                for pid, exe, proc in processes():
                    if exe.endswith("/usr/lib/astrlink/astrlink") and "app" not in found:
                        values = environment(proc)
                        appdir = values.get("APPDIR", "")
                        require(values.get("APPIMAGE") == str(appimage),
                                f"The app runs with APPIMAGE={values.get('APPIMAGE')!r}, not {appimage}")
                        require(exe == f"{appdir}/usr/lib/astrlink/astrlink",
                                f"The app runs from {exe}, outside APPDIR {appdir!r}")
                        require(os.readlink(proc / "cwd") == f"{appdir}/usr",
                                "The app does not run in the image's usr directory")
                        found["app"] = (pid, exe)
                    elif "app" in found:
                        appdir = found["app"][1].removesuffix("/usr/lib/astrlink/astrlink")
                        webkit = f"{appdir}/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/"
                        for name, path in (
                            ("WebKitWebProcess", webkit + "WebKitWebProcess"),
                            ("WebKitNetworkProcess", webkit + "WebKitNetworkProcess"),
                            ("astrlink-core", f"{appdir}/usr/lib/astrlink/bin/astrlink-core"),
                        ):
                            if exe == path:
                                found.setdefault(name, (pid, exe))
                time.sleep(0.5)
        finally:
            (logs / "appimage-processes.txt").write_text(
                "".join(f"{name} {pid} {exe}\n" for name, (pid, exe) in sorted(found.items())))
            try:
                os.killpg(process.pid, signal.SIGTERM)
                process.wait(timeout=15)
            except (ProcessLookupError, subprocess.TimeoutExpired):
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=15)
    require(len(found) == 4,
            f"Within {timeout} seconds the AppImage started only {sorted(found)} "
            "of the app, WebKitWebProcess, WebKitNetworkProcess and astrlink-core")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--appimage", type=Path, required=True)
    parser.add_argument("--appdir", type=Path, required=True,
                        help="the image extracted, squashfs-root")
    parser.add_argument("--logs", type=Path, required=True)
    parser.add_argument("--timeout", type=int, default=120)
    args = parser.parse_args()
    args.logs.mkdir(parents=True, exist_ok=True)
    check_libraries(args.appdir.resolve(), args.logs)
    check_start(args.appimage.resolve(), args.logs, args.timeout)
    print("Verified the AppImage: libraries, APPIMAGE, WebKitGTK helpers and Core")


if __name__ == "__main__":
    main()
