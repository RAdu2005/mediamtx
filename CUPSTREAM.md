# CupStream build of MediaMTX

This fork carries the MediaMTX build used by [CupStream](https://github.com/RAdu2005/cupstream).

- Branches are named `cupstream/<upstream release>`, e.g. `cupstream/v1.21.2`, and start from that upstream tag.
- The first commit on each branch is upstream PR [#6308](https://github.com/bluenviron/mediamtx/pull/6308)
  ("webrtc: allow pausing and resuming WHEP sessions"), cherry-picked unchanged.
- CupStream's own changes follow as separate commits (see section 3.2 of the CupStream project overview).
- Releases are tagged `<upstream release>-cupstream.<n>`, e.g. `v1.21.2-cupstream.1`, and CupStream pins that tag.

Upgrading to a new upstream release:

```sh
git fetch upstream --tags
git switch -c cupstream/vX.Y.Z vX.Y.Z
git cherry-pick <commits from the previous cupstream branch>
go generate ./... && go test -race ./internal/servers/webrtc/... ./internal/protocols/webrtc/...
git tag vX.Y.Z-cupstream.1 && git push origin cupstream/vX.Y.Z vX.Y.Z-cupstream.1
```
