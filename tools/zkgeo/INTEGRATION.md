# Integrating the batch geofence circuit with x/proofs


## 1. Public input layout (on-chain contract)

| idx | name | who checks it |
|---|---|---|
| 0 | `BindingHi` | `CheckBinding` (exists today) |
| 1 | `BindingLo` | `CheckBinding` (exists today) |
| 2 | `RecordsRoot` | **new**: must equal `checkpoint.zk_anchor.records_root` |
| 3 | `AreaCommitment` | policy: registered area, or any (hidden-area claims) |
| 4 | `WindowStart` | policy |
| 5 | `WindowEnd` | policy |
| 6 | `RecordCount` | **new**: must equal `checkpoint.record_count` (signed by the checkpoint signers) |

Today only 0 and 1 are compared to chain state, so a prover could pair a valid
binding with an unrelated records root. Inputs 2 and 6 close that gap; they are
what makes the proof a statement about THIS checkpoint's data.

