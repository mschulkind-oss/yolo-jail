{
  description = "YOLO Jail: A restricted container environment for AI agents";

  # ── Prebuilt-image binary cache (Cachix) ────────────────────────────────
  # The OCI image contains a few Linux (aarch64-linux) derivations built
  # from THIS repo's source (yolo-jail-conf, the entrypoint pkg, the stream
  # script) that are never on cache.nixos.org — so building the image on
  # macOS otherwise needs a Linux builder.  Publishing the built image to a
  # Cachix cache lets every macOS user *download* it instead (zero setup).
  #
  # NOT YET ENABLED — pending the Cachix account (see
  # docs/handoff-cachix-cache.md).  To turn on: create the cache, then
  # UNCOMMENT the block below and replace <PUBLIC_KEY> with the key Cachix
  # prints (format: yolo-jail.cachix.org-1:<base64>).  Rename `yolo-jail`
  # throughout if you claim a different cache name.
  #
  # nixConfig = {
  #   extra-substituters = [ "https://yolo-jail.cachix.org" ];
  #   extra-trusted-public-keys = [ "yolo-jail.cachix.org-1:<PUBLIC_KEY>" ];
  # };

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # OCI images are always Linux containers.  When building on macOS
        # (darwin), map to the equivalent Linux system so the image gets native
        # Linux packages (e.g. aarch64-darwin → aarch64-linux).
        # Requires a Linux builder in Nix (nix-darwin linux-builder, remote
        # builder, or container-based builder).

        # Alias pkgs.dockerTools locally as ociTools so the rest of the file
        # stays free of the 'docker' token. pkgs.dockerTools is a nixpkgs
        # naming convention for a general-purpose OCI image builder and has
        # nothing to do with Docker-the-runtime.
        ociTools = pkgs.dockerTools;
        imageSystem = builtins.replaceStrings ["-darwin"] ["-linux"] system;
        imagePkgs = nixpkgs.legacyPackages.${imageSystem};

        # Architecture-aware multilib path for LD_LIBRARY_PATH inside the image
        linuxMultilib =
          if imageSystem == "x86_64-linux" then "x86_64-linux-gnu"
          else if imageSystem == "aarch64-linux" then "aarch64-linux-gnu"
          else "${builtins.head (builtins.split "-" imageSystem)}-linux-gnu";

        # Extra packages from project config (passed via YOLO_EXTRA_PACKAGES env var).
        # String forms:
        #   "strace"           → latest, default output
        #   "gtk4.dev"         → latest, "dev" output only (one extra output max)
        # Object forms (all support an optional ``outputs`` list — e.g. ["out" "dev"]):
        #   {"name": "freetype", "nixpkgs": "<commit>"}      → pinned nixpkgs commit
        #   {"name": "freetype", "version": "2.14.1",        → version override (build from source)
        #    "url": "mirror://...", "hash": "sha256-..."}
        #   {"name": "gtk4", "outputs": ["out", "dev"]}      → latest, multiple outputs
        extraPackageSpecs = let
          raw = builtins.getEnv "YOLO_EXTRA_PACKAGES";
        in
          if raw == "" then [] else builtins.fromJSON raw;

        # Split "gtk4.dev" → { base = "gtk4"; output = "dev"; }; "gtk4" → { base = "gtk4"; output = null; }.
        # Validator (yolo check) rejects multi-dot strings, so we only handle one dot here.
        parseDottedSpec = s:
          let
            parts = builtins.filter builtins.isString (builtins.split "\\." s);
          in
            if builtins.length parts == 1
            then { base = builtins.head parts; output = null; }
            else { base = builtins.head parts; output = builtins.elemAt parts 1; };

        # Walk ``propagatedBuildInputs`` transitively starting from ``drv``,
        # deduplicating by store path.  Used to chase the *header* closure
        # when a ``.dev`` output is requested: gtk4.dev → pango.dev →
        # harfbuzz.dev, so ``pkg-config --cflags gtk4`` actually resolves.
        # genericClosure handles cycles and dedup for us.
        #
        # ``propagatedBuildInputs`` lists may contain ``null`` entries
        # (nixpkgs uses ``lib.optional cond pkg`` patterns that resolve to
        # null when disabled) and bare strings, so filter to honest
        # derivations before recursing.
        isDerivation = p:
          p != null && builtins.isAttrs p && (p ? outPath);
        propagatedClosure = drv:
          builtins.genericClosure {
            startSet = [ { key = drv.outPath; pkg = drv; } ];
            operator = item:
              map (p: { key = p.outPath; pkg = p; })
                  (builtins.filter isDerivation
                    (item.pkg.propagatedBuildInputs or []));
          };

        # Expand a selected output of ``drv`` into a list of derivations.
        # For ``dev`` specifically, also pull in the dev outputs of every
        # transitively propagated package — that's what nix-shell does
        # implicitly when you put gtk4 in buildInputs, and what users need
        # for cgo / FFI builds.  Other outputs (bin, lib, man, doc) are not
        # propagated: they don't chain the same way and propagation would
        # bloat the image with surprise content.
        expandSelected = drv: output:
          if output == "dev" then
            let
              closure = propagatedClosure drv;
              # Drop the root (we always include it explicitly so a
              # ``dev``-less package surfaces a clear Nix error rather than
              # silently disappearing) and keep every reachable package
              # that exposes a dev output.
              propagated = builtins.filter
                (i: i.key != drv.outPath && i.pkg ? dev)
                closure;
            in
              [ drv.dev ] ++ map (i: i.pkg.dev) propagated
          else
            [ drv.${output} ];

        # Resolve a derivation + optional list of output names to a list of
        # derivations.  Empty/missing outputs → just the default derivation.
        selectOutputs = drv: outs:
          if outs == null || outs == []
          then [ drv ]
          else builtins.concatMap (o: expandSelected drv o) outs;

        # Resolve each package spec to its base derivation + requested
        # output names (null → default output).  extraPackages (image
        # contents) and extraLibPackages (the /lib symlink farm) both
        # derive from this, so they can make different output choices
        # from the same spec.
        resolvedPackageSpecs = map (spec:
          if builtins.isString spec then
            let parsed = parseDottedSpec spec;
            in {
              drv = imagePkgs.${parsed.base};
              outputs = if parsed.output == null then null else [ parsed.output ];
            }
          else if spec ? nixpkgs then
            # Pinned to a specific nixpkgs commit
            let
              pinnedPkgs = import (builtins.fetchTarball {
                url = "https://github.com/NixOS/nixpkgs/archive/${spec.nixpkgs}.tar.gz";
              }) { system = imageSystem; };
            in { drv = pinnedPkgs.${spec.name}; outputs = spec.outputs or null; }
          else if spec ? version && spec ? url && spec ? hash then
            # Version override: rebuild existing package with different source
            {
              drv = imagePkgs.${spec.name}.overrideAttrs (old: {
                version = spec.version;
                src = imagePkgs.fetchurl {
                  url = spec.url;
                  hash = spec.hash;
                };
              });
              outputs = spec.outputs or null;
            }
          else
            { drv = imagePkgs.${spec.name}; outputs = spec.outputs or null; }
        ) extraPackageSpecs;

        extraPackages = builtins.concatMap
          (r: selectOutputs r.drv r.outputs) resolvedPackageSpecs;

        # Runtime-library derivations for the /lib farm.  getLib is applied
        # to the BASE derivation of each spec, never the selected outputs:
        # getLib is a no-op on an output-specified entry, so deriving the
        # farm from extraPackages made a ".dev" request — the normal way to
        # make a library *buildable* (headers + .pc) — contribute no
        # runtime .so at all, and every freshly linked binary died at
        # startup with "libfoo.so.N: cannot open shared object file".
        # A dev request also pulls its propagated closure's lib outputs:
        # expandSelected makes those packages linkable (their dev outputs
        # land in the image), so their .so's must be loadable too.
        extraLibPackages = builtins.concatMap (r:
          let
            devRequested = r.outputs != null && builtins.elem "dev" r.outputs;
            propagatedLibs = map (i: imagePkgs.lib.getLib i.pkg)
              (builtins.filter (i: i.key != r.drv.outPath)
                (propagatedClosure r.drv));
          in
            [ (imagePkgs.lib.getLib r.drv) ]
            ++ imagePkgs.lib.optionals devRequested propagatedLibs
        ) resolvedPackageSpecs;

        # Derivation for the shim scripts (plain text — built on host, runs in container)
        shims = pkgs.stdenv.mkDerivation {
          name = "yolo-shims";
          src = ./src/shims;
          installPhase = ''
            mkdir -p $out/bin
            cp * $out/bin/
            chmod +x $out/bin/*
          '';
        };

        # Derivation to provide /usr/bin/env and other standard paths.
        # `withChromium` controls whether chromium shims + font links are
        # created.  `withNestedPodman` controls rootless-podman config files
        # under /etc/containers.  Both are opt-out so the minimal image
        # variant can skip the bulky and/or unused plumbing.
        mkBinPathLinks = { withChromium ? true, withNestedPodman ? true }:
          pkgs.runCommand "bin-path-links" {} (''
          mkdir -p $out/usr/bin $out/bin $out/lib64 $out/lib $out/usr/lib $out/etc $out/usr/share/fonts $out/usr/share
          ln -s ${imagePkgs.coreutils}/bin/env $out/usr/bin/env
          ln -s ${imagePkgs.bashInteractive}/bin/bash $out/bin/bash
          ln -s ${imagePkgs.bashInteractive}/bin/sh $out/bin/sh
          ln -s ${imagePkgs.gawk}/bin/awk $out/bin/awk
          ln -s ${imagePkgs.gnused}/bin/sed $out/bin/sed
          ln -s ${imagePkgs.gnugrep}/bin/grep $out/bin/grep
          ln -s ${imagePkgs.findutils}/bin/find $out/bin/find
          # /usr/share/zoneinfo → tzdata store path.  glibc already
          # reads TZDIR, but Python's ``zoneinfo`` module + other
          # clients search the standard FHS path first, so symlink
          # it so both paths work.
          ln -s ${imagePkgs.tzdata}/share/zoneinfo $out/usr/share/zoneinfo
          # /etc/localtime and /etc/timezone are the FHS paths that
          # tools reach for when ``$TZ`` isn't set (Go's time package,
          # some Java/Ruby paths, ``date`` under ``env -i``).  The root
          # filesystem is mounted --read-only, so /etc can't be written
          # at entrypoint time; point these at /run (tmpfs) and let the
          # entrypoint populate /run/localtime + /run/timezone from the
          # host zone passed in via $TZ.  Without this, those clients
          # silently fall back to UTC and disagree with the host clock.
          ln -s /run/localtime $out/etc/localtime
          ln -s /run/timezone $out/etc/timezone
        '' + imagePkgs.lib.optionalString withChromium ''
          ln -s ${imagePkgs.chromium}/bin/chromium $out/usr/bin/chromium
          ln -s ${imagePkgs.chromium}/bin/chromium $out/usr/bin/google-chrome
          ln -s ${imagePkgs.chromium}/bin/chromium $out/usr/bin/chrome
          ln -s ${imagePkgs.fontconfig.out}/etc/fonts $out/etc/fonts
        '' + ''

          # Link the dynamic linker at conventional paths (architecture-aware)
          LINKER_BASENAME=$(basename "${imagePkgs.stdenv.cc.bintools.dynamicLinker}")
          ln -sf ${imagePkgs.stdenv.cc.bintools.dynamicLinker} $out/lib/$LINKER_BASENAME
          ln -sf ${imagePkgs.stdenv.cc.bintools.dynamicLinker} $out/lib64/$LINKER_BASENAME

          # Link shared libraries to /lib and /usr/lib for LD_LIBRARY_PATH discovery.
          # Iterates over all packages with lib outputs, including split-output packages
          # (e.g., fontconfig.lib has .so files separate from fontconfig.out which has etc/).
          # Note: glib and pango define outputs=["bin" "out" ...] so their DEFAULT output
          # is "bin" (no lib/). Must use .out explicitly to get the libraries.
          # Non-nix binaries (node, npm/pip packages) rely on LD_LIBRARY_PATH=/lib:/usr/lib
          # since they lack RPATH entries pointing into the nix store.
          for dir in $out/lib $out/usr/lib; do
            for pkg in ${imagePkgs.glibc} \
                       ${imagePkgs.stdenv.cc.cc.lib} \
                       ${imagePkgs.zlib}; do
              if [ -d "$pkg/lib" ]; then
                for f in "$pkg"/lib/lib*.so*; do
                  [ -f "$f" ] || [ -L "$f" ] || continue
                  name=$(basename "$f")
                  [ ! -e "$dir/$name" ] && ln -s "$f" "$dir/$name" 2>/dev/null || true
                done
              fi
            done
          done

          # Link shared libraries from user-added packages (yolo-jail.jsonc
          # "packages", resolved into extraLibPackages above) so a package
          # added for its .so (zbar, libdmtx, ...) — or added as ".dev" to
          # build against — is dlopen-able / discoverable on
          # LD_LIBRARY_PATH=/lib:/usr/lib.  extraLibPackages already went
          # through getLib, which picks each package's conventional
          # shared-lib output: e.g. zbar's .so lives in its separate "-lib"
          # output, and mupdf/openssl/sqlite default to a "-bin" output
          # with no lib/ at all — so a plain pkg/lib glob would find
          # nothing.  A headers-only package contributes no lib/ (the
          # `[ -d "$pkg/lib" ]` guard links nothing), and an empty packages
          # list expands to an empty for-loop.  Same idiom + [ ! -e ]
          # precedence guard as the core loop above, so a user package can
          # never shadow glibc/zlib.  Unconditional (outside withChromium)
          # so the minimal image gets it too, and placed before the
          # ldconfig step below so these libs also land in ld.so.cache.
          for dir in $out/lib $out/usr/lib; do
            for pkg in ${imagePkgs.lib.concatStringsSep " " (imagePkgs.lib.unique (map toString extraLibPackages))}; do
              if [ -d "$pkg/lib" ]; then
                for f in "$pkg"/lib/lib*.so*; do
                  [ -f "$f" ] || [ -L "$f" ] || continue
                  name=$(basename "$f")
                  [ ! -e "$dir/$name" ] && ln -s "$f" "$dir/$name" 2>/dev/null || true
                done
              fi
            done
          done
        '' + imagePkgs.lib.optionalString withChromium ''
          # Chromium graphics stack — only linked when chromium itself is in
          # the image.  The minimal variant has neither the binary nor the libs.
          for dir in $out/lib $out/usr/lib; do
            for pkg in ${imagePkgs.fontconfig.lib} \
                       ${imagePkgs.glib.out} \
                       ${imagePkgs.pango.out} \
                       ${imagePkgs.cairo} \
                       ${imagePkgs.harfbuzz} \
                       ${imagePkgs.freetype} \
                       ${imagePkgs.fribidi} \
                       ${imagePkgs.pixman} \
                       ${imagePkgs.libpng} \
                       ${imagePkgs.expat} \
                       ${imagePkgs.pcre2} \
                       ${imagePkgs.libffi}; do
              if [ -d "$pkg/lib" ]; then
                for f in "$pkg"/lib/lib*.so*; do
                  [ -f "$f" ] || [ -L "$f" ] || continue
                  name=$(basename "$f")
                  [ ! -e "$dir/$name" ] && ln -s "$f" "$dir/$name" 2>/dev/null || true
                done
              fi
            done
          done

          # Font directories: symlink into /usr/share/fonts so fontconfig finds them
          # (fontconfig's default fonts.conf includes <dir>/usr/share/fonts</dir>)
          for fontPkg in ${imagePkgs.noto-fonts-color-emoji}; do
            if [ -d "$fontPkg/share/fonts" ]; then
              for d in "$fontPkg"/share/fonts/*; do
                [ -d "$d" ] && ln -s "$d" "$out/usr/share/fonts/$(basename "$d")" 2>/dev/null || true
              done
            fi
          done
        '' + imagePkgs.lib.optionalString withNestedPodman ''
          # Podman nested container support
          echo "root:100000:65536" > $out/etc/subuid
          echo "root:100000:65536" > $out/etc/subgid

          # Podman storage config for rootless operation
          mkdir -p $out/etc/containers
          cat > $out/etc/containers/storage.conf <<STORAGE
          [storage]
          driver = "overlay"
          [storage.options.overlay]
          mount_program = "${imagePkgs.fuse-overlayfs}/bin/fuse-overlayfs"
          STORAGE

          cat > $out/etc/containers/containers.conf <<CONTAINERS
          [containers]
          cgroups = "disabled"
          default_sysctls = []
          [network]
          default_rootless_network_cmd = "slirp4netns"
          [engine]
          cgroup_manager = "cgroupfs"
          events_logger = "file"
          CONTAINERS

          cat > $out/etc/containers/policy.json <<POLICY
          {"default":[{"type":"insecureAcceptAnything"}]}
          POLICY

          cat > $out/etc/containers/registries.conf <<REGISTRIES
          # docker.io is the Docker Hub registry hostname, unrelated to
          # Docker-the-runtime; kept intentionally as the default registry.
          unqualified-search-registries = ["docker.io"]
          REGISTRIES
        '' + ''

          # /etc/ld.so.cache + /etc/ld.so.conf, populated for tools that
          # read /etc/ld.so.cache directly (`ldconfig -p`, diagnostics, and
          # any non-nix glibc binary built to use the standard cache path).
          #
          # IMPORTANT — this cache is NOT how the *runtime* loader finds
          # libraries in this image.  nixpkgs glibc's ld.so is built to read
          # its cache from `$glibc/etc/ld.so.cache` (a read-only store path
          # we can't write), and verified via `LD_DEBUG=libs` it never
          # consults /etc/ld.so.cache at all.  So runtime discovery of the
          # symlinked libs above — core, chromium, and user `packages:` —
          # relies entirely on LD_LIBRARY_PATH=/lib:/usr/lib (set in the
          # image config.Env below, and re-exported by the entrypoint,
          # run_cmd, and the MCP wrappers).  A consumer that scrubs
          # LD_LIBRARY_PATH cannot be rescued by this cache; that is a
          # documented limitation, not something ldconfig can fix here.
          #
          # The cache itself is generated at CONTAINER STARTUP by the
          # entrypoint (generate_ld_cache), not at image build time: this
          # derivation builds natively on darwin for macOS hosts (see the
          # eachDefaultSystem mapping above), where the Linux ldconfig
          # binary cannot run — a build-time cache was silently empty for
          # every macOS-built image.  The root fs is read-only at runtime,
          # so /etc/ld.so.cache is a symlink into /run (tmpfs), same
          # pattern as /etc/localtime.
          {
            echo "/lib"
            echo "/usr/lib"
            echo "/usr/lib/${linuxMultilib}"
          } > $out/etc/ld.so.conf
          ln -s /run/ld.so.cache $out/etc/ld.so.cache
        '');

        binPathLinks = mkBinPathLinks { };
        binPathLinksMinimal = mkBinPathLinks {
          withChromium = false;
          withNestedPodman = false;
        };

        # Derivation for the Python entrypoint package (runs inside Linux
        # container).  src/entrypoint/ used to be a single file; the
        # package is now a directory tree.  Copy it under
        # /lib/python/entrypoint/ so adding /lib/python to PYTHONPATH
        # exposes ``import entrypoint`` (matching the host-side import
        # used by cli.run_cmd's preflight subprocess).
        entrypointPkg = pkgs.runCommand "yolo-entrypoint-pkg" { } ''
          mkdir -p $out/lib/python/entrypoint
          cp -r ${./src/entrypoint}/. $out/lib/python/entrypoint/
        '';
        # Use pkgs.writeTextFile (host) instead of imagePkgs.writeShellScriptBin
        # so building these wrappers does not require a Linux builder on macOS.
        # The shebang is hardcoded to imagePkgs.bashInteractive's Linux store
        # path: writeTextFile only emits text on the host, but the shebang
        # string transitively pulls Linux bash into the wrapper's closure
        # (fetched from the binary cache) so the wrapper is self-contained
        # and doesn't rely on PATH or /usr/bin/env existing in the image.
        entrypoint = pkgs.writeTextFile {
          name = "yolo-entrypoint";
          executable = true;
          destination = "/bin/yolo-entrypoint";
          text = ''
            #!${imagePkgs.bashInteractive}/bin/bash
            # The entrypoint package lives at /lib/python/entrypoint/
            # inside the image (see entrypointPkg above).  Put the
            # parent ``python`` dir on PYTHONPATH and run as a module
            # so the package's relative imports resolve.
            export PYTHONPATH="${entrypointPkg}/lib/python''${PYTHONPATH:+:$PYTHONPATH}"
            exec ${imagePkgs.python313}/bin/python3 -m entrypoint "$@"
          '';
        };

        # In-jail yolo CLI wrapper — delegates to the mounted repo via uv
        yoloCli = pkgs.writeTextFile {
          name = "yolo";
          executable = true;
          destination = "/bin/yolo";
          text = ''
            #!${imagePkgs.bashInteractive}/bin/bash
            # Use the mounted repo with uv (deps are cached in persistent ~/.cache/uv)
            if [ -d /opt/yolo-jail/src ]; then
              export PYTHONPATH="/opt/yolo-jail''${PYTHONPATH:+:$PYTHONPATH}"
              exec ${imagePkgs.uv}/bin/uv run \
                --no-project \
                --python ${imagePkgs.python313}/bin/python3 \
                --with typer --with rich --with "pyjson5>=2.0.0" \
                -- python3 -c "from src.cli import main; main()" "$@"
            fi
            echo "YOLO Jail CLI: source not mounted at /opt/yolo-jail"
            echo "The yolo-jail repo is normally mounted automatically."
            exit 1
          '';
        };

        # Core packages: everything the integration test suite in
        # tests/test_jail.py actually touches, plus POSIX essentials that
        # shell scripts in src/entrypoint.py and src/shims/ rely on.
        # Shared between the full and minimal image variants.
        corePackages = [
          shims
          entrypoint
          yoloCli
          imagePkgs.bashInteractive
          imagePkgs.coreutils-full
          imagePkgs.git
          imagePkgs.ripgrep
          imagePkgs.fd
          imagePkgs.curl          # real curl for host-port-forwarding tests
          imagePkgs.cacert
          imagePkgs.mise
          imagePkgs.findutils
          imagePkgs.which
          imagePkgs.nodejs_22
          imagePkgs.python3
          imagePkgs.gh
          imagePkgs.gnused
          imagePkgs.gnugrep
          imagePkgs.gawk
          imagePkgs.gnupatch
          imagePkgs.diffutils
          imagePkgs.gzip
          imagePkgs.bzip2
          imagePkgs.xz
          imagePkgs.gnutar
          imagePkgs.unzip
          imagePkgs.zip
          imagePkgs.zlib
          imagePkgs.procps        # ps, pgrep, pkill
          imagePkgs.overmind      # exercised by overmind isolation tests
          imagePkgs.jq
          imagePkgs.uv
          imagePkgs.iptables      # DNAT rules (published port → localhost fixup)
          imagePkgs.socat         # host port forwarding into the jail
          imagePkgs.sox           # Claude Code's `/voice` recorder depends on it
          # Timezone database — without it, glibc can't resolve
          # ``TZ=America/New_York`` etc. and silently falls back to UTC,
          # so `date` inside the jail reports wall-clock time that
          # disagrees with the host.  TZDIR in the image env below
          # points glibc at this store path.
          imagePkgs.tzdata
        ];

        # Extras that bulk the image up but aren't exercised by the
        # integration test suite.  Kept out of the minimal variant so CI
        # integration runs don't need to load ~2 GB of unused bytes.
        fullPackages = [
          imagePkgs.openssh
          imagePkgs.strace
          imagePkgs.lsof
          imagePkgs.file
          imagePkgs.gcc
          imagePkgs.gnumake
          imagePkgs.binutils
          imagePkgs.chromium                   # For both MCP and Playwright
          imagePkgs.fontconfig
          imagePkgs.noto-fonts-color-emoji     # Emoji font for Chromium rendering
          imagePkgs.glibc.bin                  # ldd
          imagePkgs.net-tools                  # netstat
          imagePkgs.iproute2                   # ss, ip
          imagePkgs.iputils                    # ping
          imagePkgs.dnsutils                   # dig, host, nslookup
          imagePkgs.htop
          imagePkgs.hivemind
          imagePkgs.tmux
          imagePkgs.bat
          imagePkgs.eza
          imagePkgs.delta
          imagePkgs.fzf
          imagePkgs.nix                        # nested nix builds inside jail
          imagePkgs.podman                     # nested container support
          imagePkgs.fuse-overlayfs             # storage driver for rootless podman
          imagePkgs.slirp4netns                # rootless networking for nested podman
          imagePkgs.shadow                     # newuidmap/newgidmap
        ];

        mkOciImage = { minimal ? false }:
          ociTools.streamLayeredImage {
            name = "yolo-jail";
            tag = if minimal then "ci-minimal" else "latest";
            created = "now";
            maxLayers = 100;

            contents =
              [ (if minimal then binPathLinksMinimal else binPathLinks) ]
              ++ corePackages
              ++ (if minimal then [] else fullPackages)
              ++ extraPackages;

            # Create directories needed by nested podman and general operation
            fakeRootCommands = ''
              mkdir -p ./var/tmp ./var/cache ./var/log ./run ./var/lib/containers

              # Pre-create mountpoint directories for --read-only root filesystem.
              # With --read-only, the OCI runtime cannot create these on the fly.
              mkdir -p ./home/agent ./workspace ./tmp ./opt/yolo-jail ./mise
              mkdir -p ./ctx/host-claude ./ctx/host-nvim-config
              mkdir -p ./nix/var/nix/daemon-socket

              # Podman needs /etc/passwd and /etc/group
              echo 'root:x:0:0:root:/home/agent:/bin/bash' > ./etc/passwd
              echo 'root:x:0:' > ./etc/group
              echo 'nixbld:x:30000:' >> ./etc/group
            '';

            config = {
              Cmd = [ "/bin/bash" ];
              # We explicitly place shims first in PATH
              Env = [
                "PATH=${shims}/bin:/bin:/usr/bin"
                "SSL_CERT_FILE=${imagePkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
                "LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/${linuxMultilib}"
                # FHS-style pkg-config search path for .pc files laid down
                # by any ``.dev`` outputs in the image (gtk4.dev,
                # freetype.dev, ...).  Without this, ``pkg-config --cflags
                # foo`` fails even when /lib/pkgconfig/foo.pc exists,
                # because pkg-config's compiled-in default only looks in
                # its own nix-store path.
                "PKG_CONFIG_PATH=/lib/pkgconfig:/share/pkgconfig:/usr/lib/pkgconfig"
                "FONTCONFIG_FILE=/etc/fonts/fonts.conf"
                "FONTCONFIG_PATH=/etc/fonts"
                # Point glibc at the tzdata store path so ``TZ=<zone>``
                # passed from the host resolves (otherwise date + glibc
                # fall back to UTC, diverging from the host clock).
                "TZDIR=${imagePkgs.tzdata}/share/zoneinfo"
              ];
              WorkingDir = "/workspace";
            };
          };

        ociImage = mkOciImage { minimal = false; };
        ociImageMinimal = mkOciImage { minimal = true; };

      in
      {
        packages.default = ociImage;
        packages.ociImage = ociImage;
        packages.ociImageMinimal = ociImageMinimal;
        # The /lib symlink farm alone — buildable in seconds, so tests and
        # humans can assert lib discovery (e.g. that a "foo.dev" package
        # spec still lands libfoo.so in /lib) without building an image.
        packages.binPathLinks = binPathLinksMinimal;

        devShells.default = pkgs.mkShell {
          buildInputs = [
            pkgs.just
          ];
        };
      }
    );
}
