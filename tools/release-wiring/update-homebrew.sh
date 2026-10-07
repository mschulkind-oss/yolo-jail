#!/usr/bin/env bash
# Trusted-main-only Homebrew formula publisher. RELEASE_TAG and VERSION are
# validated immutable data; no target checkout or code is present in this job.
set -euo pipefail
: "${VERSION:?VERSION is required}"
: "${RELEASE_TAG:?RELEASE_TAG is required}"
: "${HOMEBREW_TAP_TOKEN:?HOMEBREW_TAP_TOKEN is required}"
if ! printf '%s\n' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || [ "$RELEASE_TAG" != "v${VERSION}" ]; then
  echo "✗ Refusing Homebrew update for a malformed or mismatched immutable version/tag; no tap write occurred." >&2
  exit 1
fi
TARBALL="https://github.com/mschulkind-oss/yolo-jail/archive/refs/tags/${RELEASE_TAG}.tar.gz"
SHA256=$(curl -fsSL --retry 3 "$TARBALL" | sha256sum | cut -d' ' -f1)
if ! printf '%s\n' "$SHA256" | grep -Eq '^[0-9a-f]{64}$'; then
  echo "✗ Bad SHA-256 for ${TARBALL}: ${SHA256}" >&2
  exit 1
fi

cat > yolo-jail.rb <<FORMULA
class YoloJail < Formula
  desc "Declarative agentic development environments, from a sealed jail to your host"
  homepage "https://github.com/mschulkind-oss/yolo-jail"
  url "${TARBALL}"
  sha256 "${SHA256}"
  license "Apache-2.0"

  depends_on "go" => :build

  def install
    ldflags = %W[
      -s -w
      -X github.com/mschulkind-oss/yolo-jail/internal/version.buildVersion=#{version}
    ]
    system "go", "build", *std_go_args(output: bin/"yolo", ldflags: ldflags.join(" ")), "./cmd/yolo"

    # Stage the prebuilt source bundle beside the binary for checkout-less installs.
    with_env("VERSION" => version.to_s) do
      system "scripts/stage-source-bundle.sh", pkgshare.to_s
    end
  end

  def caveats
    <<~EOS
      The first \`yolo\` run in a workspace builds or pulls the jail's
      container image (nix), which takes a while; later runs reuse it.
    EOS
  end

  test do
    assert_match "yolo-jail #{version}", shell_output("#{bin}/yolo --version")
  end
end
FORMULA

git clone "https://x-access-token:${HOMEBREW_TAP_TOKEN}@github.com/mschulkind-oss/homebrew-tap.git" tap
mkdir -p tap/Formula
cp yolo-jail.rb tap/Formula/yolo-jail.rb
cd tap
git config user.name "yolo-jail release bot"
git config user.email "noreply@github.com"
git add Formula/yolo-jail.rb
if git diff --cached --quiet; then
  echo "Formula unchanged; nothing to commit."
  exit 0
fi
git commit -m "yolo-jail ${VERSION}"
git push
