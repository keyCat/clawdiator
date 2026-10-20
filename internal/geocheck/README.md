# geocheck: a copy for clawdiator

This folder is a copy of the `geocheck` module of the warmup project.
The copy comes from the warmup commit `ddc1d91` of 2026-10-15. On 2026-10-20, each file was the same as in that commit.

clawdiator builds from this copy, so a build on GitHub does not need the warmup repository.
The command `cmd/geocheck` stays in warmup, because clawdiator does not use it.

The 2 copies do not update each other. Follow these rules:

1. If you change `geocheck` in warmup, decide if clawdiator needs the change.
2. To take a change, copy the changed files into this folder. Then write the new warmup commit in this file.
3. If you change a file here, make the same change in warmup. If the copies must differ, write the reason in this file.
