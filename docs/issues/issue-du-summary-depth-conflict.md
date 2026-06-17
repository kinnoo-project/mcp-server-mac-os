**issue**
The original MVP exposed `du` with both a `summary` switch (`-s`) and a `max_depth` switch (`-d N`), guarded in handler code so only one was ever emitted. The new generic argument builder emits each parameter independently and cannot express "if summary is set, skip max_depth." Emitting both would produce `du -s -d N`, which macOS `du` rejects as mutually exclusive — a latent bug if both were ever supplied together. Encountered during Slice C.

**fixed**
Redesigned the `du` capability to drop the `summary` boolean entirely and use `max_depth: 0` for the per-argument total (the summary view): `du -d 0 <path>` produces the same single total line as `du -s <path>`. This removes the conflicting-flag possibility, keeps `du` fully declarative (generic builder, no custom Go code), and is explained in the capability/parameter descriptions.
