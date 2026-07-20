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
  # docs/implementation/handoff-cachix-cache.md).  To turn on: create the cache, then
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

        # ── Go-port binary cross-compile (go-port plan §3) ─────────────────
        # Static, CGO-free cross-compile of every cmd/ binary to the image's
        # Linux target arch, using the HOST Go toolchain (pkgs.go, not
        # imagePkgs).  Go cross-compiles natively (aarch64-darwin ->
        # aarch64-linux) with zero Linux builder, preserving the flake's
        # no-Linux-builder property.  vendorHash=null + committed vendor/
        # (once deps exist) keeps the --impure eval reproducible.
        goArch =
          if imageSystem == "x86_64-linux" then "amd64"
          else if imageSystem == "aarch64-linux" then "arm64"
          else builtins.head (builtins.split "-" imageSystem);
        # Only the files that affect a Go build — keeps the derivation from
        # rebuilding when unrelated repo files (docs, Python) change.
        goSrc = pkgs.lib.fileset.toSource {
          root = ./.;
          fileset = pkgs.lib.fileset.unions (
            [ ./go.mod ]
            ++ pkgs.lib.optionals (builtins.pathExists ./go.sum) [ ./go.sum ]
            ++ pkgs.lib.optionals (builtins.pathExists ./vendor) [ ./vendor ]
            ++ pkgs.lib.optionals (builtins.pathExists ./cmd) [ ./cmd ]
            ++ pkgs.lib.optionals (builtins.pathExists ./internal) [ ./internal ]
          );
        };
        goBinaries = pkgs.stdenv.mkDerivation {
          pname = "yolo-jail-go";
          version = "0-dev";
          src = goSrc;
          nativeBuildInputs = [ pkgs.go ];
          # Hermetic: no network in the Nix sandbox. Vendored deps (or an
          # empty module graph) satisfy `-mod` without a proxy fetch.
          buildPhase = ''
            runHook preBuild
            export HOME=$TMPDIR
            export GOCACHE=$TMPDIR/go-cache
            export GOFLAGS=''${GOFLAGS:-}
            export CGO_ENABLED=0
            export GOOS=linux
            export GOARCH=${goArch}
            [ -d vendor ] && export GOFLAGS="-mod=vendor $GOFLAGS" || export GOPROXY=off
            mkdir -p $out/bin
            for d in cmd/*/; do
              name="$(basename "$d")"
              echo "go build $name -> $out/bin/$name (linux/${goArch})"
              go build -trimpath -o "$out/bin/$name" "./$d"
            done
            runHook postBuild
          '';
          dontInstall = true;
          dontFixup = true;
        };

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

        # ── Native aarch64-darwin resolution (macos-user backend) ──────────
        # Mirror resolvedPackageSpecs but resolve against `pkgs` (the flake's
        # own `system` — aarch64-darwin on a Mac) instead of imagePkgs, and
        # thread `system` (NOT imageSystem) into the pinned/version branches.
        # Each spec is wrapped in tryEval + guarded by lib.meta.availableOn so
        # a package with no darwin build is SKIPPED (warn-and-skip) instead of
        # failing the whole shell.  Same YOLO_EXTRA_PACKAGES contract as the
        # image path.  Only NEW outputs are added below; the image path is
        # untouched, so Linux eval stays safe (empty specs → empty results).
        darwinSpecName = spec:
          if builtins.isString spec then (parseDottedSpec spec).base
          else spec.name;
        darwinResolved = map (spec:
          let
            name = darwinSpecName spec;
            parsed = if builtins.isString spec then parseDottedSpec spec else null;
            # Source attrset for the base attr: pinned specs fetch their own
            # nixpkgs (system = darwin), everything else resolves from `pkgs`.
            #
            # LIMITATION (warn-and-skip covers PLATFORM availability only): a
            # pinned {nixpkgs:<rev>} whose fetchTarball fails (bad/deleted rev,
            # offline) or a {version,url,hash} override with a bad hash aborts
            # the WHOLE eval — builtins fetch/IO errors are NOT catchable by
            # tryEval (verified).  So pinned/override specs are all-or-nothing,
            # same as the image path; only plain-string specs get graceful
            # skip.  The CLI (darwin_packages.materialize) translates the raw
            # nix abort into an actionable message.  A lock-time fix (pinned
            # nixpkgs as flake inputs) is future work — see
            # docs/qa/macos-user-review-findings.md #2/#7.
            src =
              if (!builtins.isString spec) && spec ? nixpkgs then
                import (builtins.fetchTarball {
                  url = "https://github.com/NixOS/nixpkgs/archive/${spec.nixpkgs}.tar.gz";
                }) { inherit system; }
              else pkgs;
            attr = if builtins.isString spec then parsed.base else spec.name;
            # `?` membership NEVER throws for a missing attr (tryEval can't
            # catch attribute-missing errors — only `throw`/`assert`), so this
            # guard, not tryEval, is what makes an unknown package skippable.
            present = src ? ${attr};
            baseDrv = if present then src.${attr} else null;
            drv =
              if !present then null
              else if (!builtins.isString spec)
                   && spec ? version && spec ? url && spec ? hash then
                baseDrv.overrideAttrs (old: {
                  version = spec.version;
                  src = pkgs.fetchurl { url = spec.url; hash = spec.hash; };
                })
              else baseDrv;
            outputs =
              if builtins.isString spec then
                (if parsed.output == null then null else [ parsed.output ])
              else spec.outputs or null;
            # availableOn reads meta.platforms/badPlatforms (never builds).
            # Wrap in tryEval to absorb a package whose meta itself throws.
            okAttempt =
              if !present then { success = true; value = false; }
              else builtins.tryEval
                (pkgs.lib.meta.availableOn { inherit system; } drv);
          in {
            inherit name outputs drv;
            available = present && okAttempt.success && okAttempt.value;
          }
        ) extraPackageSpecs;

        darwinKept = builtins.filter (r: r.available) darwinResolved;
        darwinSkippedNames =
          map (r: r.name) (builtins.filter (r: !r.available) darwinResolved);
        darwinPackages =
          builtins.concatMap (r: selectOutputs r.drv r.outputs) darwinKept;

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

        # Use pkgs.writeTextFile (host) instead of imagePkgs.writeShellScriptBin
        # so building these wrappers does not require a Linux builder on macOS.
        # The shebang is hardcoded to imagePkgs.bashInteractive's Linux store
        # path: writeTextFile only emits text on the host, but the shebang
        # string transitively pulls Linux bash into the wrapper's closure
        # (fetched from the binary cache) so the wrapper is self-contained
        # and doesn't rely on PATH or /usr/bin/env existing in the image.

        # Entrypoint wrapper — prefers the dev override at
        # /opt/yolo-jail/dist-go/ (live-mount iteration, no image rebuild)
        # then falls back to the baked Go binary.
        entrypoint = pkgs.writeTextFile {
          name = "yolo-entrypoint";
          executable = true;
          destination = "/bin/yolo-entrypoint";
          text = ''
            #!${imagePkgs.bashInteractive}/bin/bash
            dev_override="/opt/yolo-jail/dist-go/linux-${goArch}/yolo-entrypoint"
            if [ -x "$dev_override" ]; then
              exec "$dev_override" "$@"
            fi
            exec ${goBinaries}/bin/yolo-entrypoint "$@"
          '';
        };

        # In-jail yolo CLI — prefers the dev override, then the baked
        # Go binary.
        yoloCli = pkgs.writeTextFile {
          name = "yolo";
          executable = true;
          destination = "/bin/yolo";
          text = ''
            #!${imagePkgs.bashInteractive}/bin/bash
            dev_override="/opt/yolo-jail/dist-go/linux-${goArch}/yolo"
            if [ -x "$dev_override" ]; then
              exec "$dev_override" "$@"
            fi
            exec ${goBinaries}/bin/yolo "$@"
          '';
        };

        # Expose all Go cmd/* binaries (except yolo and yolo-entrypoint,
        # which have their own wrappers with dev-override logic, and goprobe,
        # which is a dev-only deployment tripwire and must not pollute the
        # runtime PATH) in /bin/.
        goBinariesLinks = pkgs.runCommand "go-bin-links" { } ''
          mkdir -p $out/bin
          for bin in ${goBinaries}/bin/*; do
            name=$(basename "$bin")
            case "$name" in
              yolo|yolo-entrypoint|goprobe) continue ;;
            esac
            ln -s "$bin" "$out/bin/$name"
          done
        '';

        # Core packages: everything the integration test suite in
        # tests/test_jail.py actually touches, plus POSIX essentials.
        # Shared between the full and minimal image variants.
        corePackages = [
          entrypoint
          yoloCli
          goBinariesLinks
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
              # Default PATH for anything that runs before the Go entrypoint
              # resets it.  Blocked-tool shims are generated at boot by the
              # entrypoint into $HOME/.yolo-shims (config-driven) and prepended
              # to PATH there — there is no baked shim layer any more.
              Env = [
                "PATH=/bin:/usr/bin"
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

        # ── Container-based Linux builder (macOS container-runtime path) ────
        # On the container runtime (podman/Apple Container), when a `packages:`
        # build isn't cached, nix must offload to Linux.  Instead of a separate
        # QEMU VM (darwin.linux-builder — on the roadmap as a fallback), run a
        # tiny nix+sshd container ON THE RUNTIME THAT'S ALREADY UP: ephemeral,
        # no second hypervisor, zero idle RAM.  nix's ssh-ng remote-builder
        # protocol just runs `nix-daemon --stdio` over ssh, so the image needs
        # exactly: nix, openssh sshd, a `builder` user, and an authorized key.
        #
        # Built with imagePkgs (Linux target: aarch64-linux on a Mac, native in
        # CI) so no Mac ever builds it — CI publishes it to GHCR and the Mac
        # pulls it (no chicken-and-egg).  The image is KEYLESS: the builder's
        # authorized key is injected at container-run time via the
        # YOLO_BUILDER_PUBKEY env var (see the entrypoint), NOT baked in — so
        # the same published image serves every user and the host daemon's
        # private key is generated per-setup and never in the image.
        builderImage =
          let
            ip = imagePkgs;
            # sshd config: key-only, force `nix-daemon --stdio` (ssh-ng), no
            # TTY/agent/forwarding — a build-only endpoint, nothing else.
            sshdConfig = ip.writeText "sshd_config" ''
              Port 22
              Protocol 2
              HostKey /etc/ssh/ssh_host_ed25519_key
              # Key-only root login: the build endpoint runs nix-daemon as root
              # (which owns /nix/store).  This matches the reference nix-builder
              # images (LnL7/nix:ssh) and sidesteps drop-privs key-read issues.
              PermitRootLogin prohibit-password
              PasswordAuthentication no
              KbdInteractiveAuthentication no
              PubkeyAuthentication yes
              AuthorizedKeysFile /etc/ssh/authorized_keys.d/%u
              AllowTcpForwarding no
              X11Forwarding no
              AllowAgentForwarding no
              PermitTTY no
              # Every session is a nix-daemon build channel; nothing else runs.
              ForceCommand ${ip.nix}/bin/nix-daemon --stdio
              Subsystem sftp /dev/null
            '';
            # Entry script: generate a host key on first boot, drop the baked
            # authorized key for root, then exec sshd in the foreground.
            entrypoint = ip.writeShellScript "yolo-builder-entrypoint" ''
              set -eu
              mkdir -p /etc/ssh/authorized_keys.d /nix/var/nix/{profiles,gcroots}
              [ -f /etc/ssh/ssh_host_ed25519_key ] || \
                ${ip.openssh}/bin/ssh-keygen -q -t ed25519 -N "" \
                  -f /etc/ssh/ssh_host_ed25519_key
              printf '%s\n' "$YOLO_BUILDER_PUBKEY" > /etc/ssh/authorized_keys.d/root
              chmod 600 /etc/ssh/authorized_keys.d/root
              exec ${ip.openssh}/bin/sshd -D -e -f ${sshdConfig}
            '';
          in
          ociTools.streamLayeredImage {
            name = "yolo-jail-builder";
            tag = "latest";
            # Contents: nix (the builder), sshd, a shell + coreutils for build
            # steps, and CA certs for substituter fetches.  This closure IS the
            # size floor — a nix builder must contain nix; no base distro
            # (alpine included) gets under it.
            contents = [
              ip.nix
              ip.openssh
              ip.bashInteractive
              ip.coreutils
              ip.cacert
              # /etc/passwd + /etc/group with root + a `builder` user so sshd
              # has a real account to run the forced command as.
              (ip.runCommand "yolo-builder-etc" { } ''
                mkdir -p $out/etc/nix $out/etc/ssh/authorized_keys.d \
                         $out/var/empty $out/tmp $out/root $out/home/builder
                cat > $out/etc/passwd <<EOF
                root:x:0:0:root:/root:${ip.bashInteractive}/bin/bash
                builder:x:1000:1000:nix builder:/home/builder:${ip.bashInteractive}/bin/bash
                sshd:x:74:74:sshd privsep:/var/empty:/sbin/nologin
                EOF
                cat > $out/etc/group <<EOF
                root:x:0:
                builder:x:1000:
                nixbld:x:30000:builder
                sshd:x:74:
                EOF
                # A trusted builder user so it may run nix-daemon --stdio.
                cat > $out/etc/nix/nix.conf <<EOF
                experimental-features = nix-command flakes
                trusted-users = root builder
                sandbox = false
                build-users-group =
                EOF
              '')
            ];
            config = {
              Cmd = [ "${entrypoint}" ];
              ExposedPorts = { "22/tcp" = { }; };
              Env = [
                "NIX_PATH=nixpkgs=${ip.path}"
                "USER=root"
                "HOME=/root"
                # CA bundle so substituter fetches (builders-use-substitutes)
                # don't fail with "Problem with the SSL CA cert" during builds.
                "NIX_SSL_CERT_FILE=${ip.cacert}/etc/ssl/certs/ca-bundle.crt"
                "SSL_CERT_FILE=${ip.cacert}/etc/ssl/certs/ca-bundle.crt"
              ];
            };
          };
      in
      {
        packages.default = ociImage;
        packages.ociImage = ociImage;
        packages.ociImageMinimal = ociImageMinimal;
        packages.builderImage = builderImage;
        # go-port Stage 0 walking skeleton: static Linux Go binaries,
        # cross-compiled with no Linux builder. Buildable in-jail today to
        # prove the channel; baked into the image at Stage 10/11.
        packages.goBinaries = goBinaries;
        # The /lib symlink farm alone — buildable in seconds, so tests and
        # humans can assert lib discovery (e.g. that a "foo.dev" package
        # spec still lands libfoo.so in /lib) without building an image.
        packages.binPathLinks = binPathLinksMinimal;

        devShells.default = pkgs.mkShell {
          buildInputs = [
            pkgs.just
          ];
        };

        # ── macos-user backend: native darwin package materialization ──────
        # yoloDarwinPackages is a buildEnv (profile) whose single `/bin` holds
        # EXACTLY the aarch64-darwin build of `packages:` (from
        # YOLO_EXTRA_PACKAGES) — NOT a devShell.  A devShell's `print-dev-env`
        # would dump the whole stdenv toolchain (clang, GNU coreutils/sed/grep,
        # make, …) onto the agent PATH ahead of the macOS BSD userland; a
        # buildEnv contains only the declared packages.  The CLI realizes it
        # with `nix build --print-out-paths` and puts `<out>/bin` on the
        # sandboxed agent's PATH — no VM, no Linux image.  Packages with no
        # darwin build are filtered out and surfaced via
        # darwinUnavailablePackages (warn-and-skip).  See internal/darwinpkg.
        packages.yoloDarwinPackages = pkgs.buildEnv {
          name = "yolo-darwin-packages";
          paths = darwinPackages;
          # Merge pkg-config metadata so PKG_CONFIG_PATH can point at one dir.
          extraOutputsToInstall = [ "bin" "lib" "dev" ];
        };
        darwinUnavailablePackages = darwinSkippedNames;
      }
    );
}
