"""Per-operation verdict on one text unit, shared by every plugin that edits text in place."""
from __future__ import annotations

from .plan import Op


def judge(before: str, after: str, ops: list[Op]) -> dict[str, tuple[bool, bool]]:
    """Per-operation verdict for one paragraph: (executed, residual).

    The candidate paragraph must be the input with each planned range replaced, so it is
    walked left to right as kept text, replacement, kept text... Each operation is then
    judged on its own: its replacement is there (executed), or all or part of the original
    is still there (residual: the unit holds approved content that was not removed), and
    a layout that does not fit at all marks every operation unaccounted. A paragraph that
    differs from the plan anywhere else is never reported as successfully executed.
    """
    ordered = sorted(ops, key=lambda o: o.start)
    unaccounted = {op.id: (False, False) for op in ops}
    verdict: dict[str, tuple[bool, bool]] = {}
    ic = cc = 0

    def fits(at, nxt, final):
        return after[at:at + len(nxt)] == nxt and (not final or len(after) == at + len(nxt))

    for index, op in enumerate(ordered):
        kept, original = before[ic:op.start], before[op.start:op.end]
        if not after.startswith(kept, cc):
            return unaccounted
        cc += len(kept)
        ic = op.end
        final = index + 1 == len(ordered)
        nxt = before[op.end:len(before) if final else ordered[index + 1].start]
        replacement = op.replacement
        # a fragment of the original left in front of the replacement
        lead = 0
        if replacement and not after.startswith(replacement, cc):
            for k in range(len(original) - 1, 0, -1):
                if after.startswith(original[:k] + replacement, cc):
                    lead = k
                    break
        if lead:
            cc += lead + len(replacement)
            residual = True
        elif replacement and after.startswith(replacement, cc):
            cc += len(replacement)
            residual = False
        elif original and after.startswith(original, cc):
            verdict[op.id] = (False, True)
            cc += len(original)
            continue
        elif not replacement:
            residual = False
        else:
            return unaccounted
        # a fragment of the original left behind the replacement
        if not fits(cc, nxt, final):
            for k in range(len(original) - 1, 0, -1):
                if after.startswith(original[-k:], cc) and fits(cc + k, nxt, final):
                    cc += k
                    residual = True
                    break
            else:
                return unaccounted
        verdict[op.id] = (not residual, residual)
    if after[cc:] != before[ic:]:
        return unaccounted
    return verdict
