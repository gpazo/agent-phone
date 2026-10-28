# Design arena synthesis

Parent scores against rubric (continuous frames, isolation/lifecycle, bounded cost, small shared API, proof/freshness): A 5/4/5/3/5 = 22; B 5/5/4/5/4 = 23. Cross-judge scored A 5/5/4/4/5 = 23 and B 4/4/4/4/3 = 19, recommending A for sampling and precise lifecycle. Parent read both rationales and resolves disagreement by keeping B’s per-phone process/interface boundary while explicitly adopting A’s validated sampling and retirement requirements; those were the judge’s reasons for downgrading B, not measured benefits of multiplexing.

Selected base B: a latest-frame hub with one native process per active phone. The prototype proved this capture shape on all four with modest observed process RSS. It isolates private-API failures to one device and needs no bidirectional multiplexed native protocol. The 8GB constraint does not justify a more complex shared native process before actual measurements show a problem.

Graft A's strict epoch retirement, source timestamps before readback, bounded idle grace for repeated agent reads, and fully decoded off-main image publication. Both designs converge on independent read-only frame state, latest-only delivery, and one shared producer per device across clients. Reject opaque mixed image/status cursor and pixel-change hashing for the first version: an explicit epoch/sequence cursor waits for a newer sampled frame, even if pixels repeat. This is simpler to explain and verify; semantic UI completion still comes from AX and the image.

Native strategy follows the proven prototype: bounded in-process sampling at 4Hz and 402px width, with one encode at a time per phone. No per-frame process/files. Defer callback optimization and codecs until measurements justify them. The initial 4fps cap is a resource policy, not a performance claim.

Implementation contract lives in implementation-task.md. Production correctness and resource measurements remain gates after implementation. Source has no HEAD/remote, so candidates use isolated scratch source copies and preserve an untouched baseline.

Cross-judge confirms observed ~68MB combined probe RSS does not justify multiplexing on memory grounds. Neither model family diversity nor comparative native architecture benchmarks are available. The shared implementation task encodes the accepted lifecycle and timestamp grafts; no second capture backend will ship.
