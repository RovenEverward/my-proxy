package fusion

import _ "embed"

// GPL license is copied verbatim from the official v0.2.12 source release.
//
//go:embed LICENSE.goecs
var License []byte

const Notice = `Go ECS (Fusion Monster), oneclickvirt/ecs v0.2.12
Unmodified official executable, distributed separately under GPL-3.0.
No warranty. See LICENSE.goecs beside this executable.
Official release: https://github.com/oneclickvirt/ecs/releases/tag/v0.2.12
Official published source baseline: https://github.com/oneclickvirt/ecs/archive/refs/tags/v0.2.12.tar.gz
Source commit: 929806d8bc304d70a6700c13582d3e922aec99f3
Official binaries report v0.2.12+dirty and vcs.modified=true; the commit above
is the official published source baseline, not a claim of a pristine build.
Source build definitions and dependency versions: go.mod,
.github/workflows/build_binary.yaml, .goreleaser.yaml, scripts/embed_geekbench.sh
Component source pins and embedded assets are documented in these build definitions.
`
