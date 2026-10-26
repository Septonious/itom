"""Build the pinned Go Android AAR on the user's machine, before Gradle."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import build
import build_core

MOBILE = "v0.0.0-20260908204917-8b95e45f8d3e"
NDK = "28.2.13676358"

def required_android_packages(sdk: Path):
    packages = []
    if not (sdk / "platforms/android-29/android.jar").is_file(): packages.append("platforms;android-29")
    if not (sdk / "ndk" / NDK / "source.properties").is_file(): packages.append("ndk;" + NDK)
    return packages

def run(root: Path, cache: Path, sdk: Path, environment: dict, accept: bool, metadata: dict):
    env = environment.copy()
    for key in ("GOROOT", "GOOS", "GOARCH", "GOARM", "GOAMD64", "GOARM64", "CGO_ENABLED", "GOEXPERIMENT", "GODEBUG", "ANDROID_NDK_ROOT"):
        env.pop(key, None)
    go, _ = build_core.ensure_go(cache)
    tools = cache / ("mobile-" + MOBILE)
    tools.mkdir(parents=True, exist_ok=True)
    env.update(GOTOOLCHAIN="local", GOFLAGS="-buildvcs=false", GOBIN=str(tools),
               GOMODCACHE=str(cache / "gomod"), GOCACHE=str(cache / "gocache"),
               GOPATH=str(cache / "gopath"), ANDROID_NDK_HOME=str(sdk / "ndk" / NDK))
    env["PATH"] = str(tools) + os.pathsep + str(go.parent) + os.pathsep + env.get("PATH", "")
    # gomobile init installs gobind@latest. Provision the workspace and install
    # both tools at the explicit pin instead; this AAR does not use OpenAL.
    (cache / "gopath/pkg/gomobile").mkdir(parents=True, exist_ok=True)
    version = subprocess.run([str(go), "version"], env=env, check=True, capture_output=True, text=True, encoding="utf-8").stdout
    if not version.startswith("go version go" + build_core.GO_VERSION + " "):
        raise RuntimeError("Unexpected Go toolchain in cache")
    selected = subprocess.run([str(go), "list", "-mod=readonly", "-m", "-json", "github.com/emersion/go-imap/v2"],
                              cwd=root / "native", env=env, check=True, capture_output=True,
                              text=True, encoding="utf-8").stdout
    build_core.verify_imap_replacement(json.loads(selected), root / "core", "../core/third_party/go-imap")
    suffix = ".exe" if os.name == "nt" else ""
    manager = sdk / "cmdline-tools" / build.CMDLINE_TOOLS / "bin" / ("sdkmanager.bat" if os.name == "nt" else "sdkmanager")
    build.normalize_sdk_package(sdk / "platforms/android-29", "android.jar")
    build.normalize_sdk_package(sdk / "ndk" / NDK, "build/cmake/android.toolchain.cmake")
    # The pinned gomobile parser needs a real integer-named platform.
    for package in required_android_packages(sdk):
        subprocess.run([str(manager), "--sdk_root=" + str(sdk), package], check=True, env=env,
                       text=True, encoding="utf-8", input=("y\n" * 200) if accept else None)
    build.normalize_sdk_package(sdk / "platforms/android-29", "android.jar")
    build.normalize_sdk_package(sdk / "ndk" / NDK, "build/cmake/android.toolchain.cmake")
    out = root / "android/app/libs/itom.aar"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.unlink(missing_ok=True)
    log = root / "dist/native-build.log"
    with log.open("w", encoding="utf-8") as stream:
        def command(args):
            print("Native:", " ".join(str(x) for x in args), flush=True)
            stream.write("$ " + " ".join(str(x) for x in args) + "\n"); stream.flush()
            result = subprocess.run([str(x) for x in args], cwd=root / "native", env=env, stdout=stream, stderr=subprocess.STDOUT)
            if result.returncode:
                raise RuntimeError("Android native build failed. Log: " + str(log))
        for tool in ("gomobile", "gobind"):
            if not (tools / (tool + suffix)).is_file():
                command([go, "install", "golang.org/x/mobile/cmd/" + tool + "@" + MOBILE])
        command([go, "mod", "download"])
        command([go, "mod", "verify"])
        # Host-only cross-module bridge fixture is explicitly enabled for tests,
        # never for gomobile bind or the production server.
        command([go, "test", "-mod=readonly", "-tags=itomtest", "-count=1", "-timeout=100s", "./..."])
        command([tools / ("gomobile" + suffix), "bind", "-trimpath", "-ldflags=-s -w", "-target=android/arm64,android/arm",
                 "-androidapi=29", "-javapkg=org.itom.nativecore", "-o", out, "."])
    if not out.is_file():
        raise RuntimeError("Native builder did not produce itom.aar")
    metadata["native"] = {"gomobile": MOBILE, "ndk": NDK, "aar_sha256": hashlib.sha256(out.read_bytes()).hexdigest(),
                          "targets": ["android/arm64", "android/arm"], "api": 29}
