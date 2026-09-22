# Unpriced calls

AstrLink estimates API-equivalent cost from recorded upstream usage and the
official price snapshot selected for each call. This estimate is separate from
the upstream service's actual charge. Calls without enough data stay unpriced
and are excluded from the displayed monetary total.

Gemini usage reads `promptTokensDetails` and `candidatesTokensDetails` for audio
counts. A complete modality breakdown can establish zero audio tokens; a missing
or partial breakdown remains unknown. Output totals include thinking tokens.

Some price formulas, including Gemini 3.7 Flash's current formula, charge text
and audio input at the same rate. The evaluator uses exact rational arithmetic
to prove whether the split can affect the amount or selected tier. If it cannot,
the known total is sufficient even without audio details. Different audio rates,
audio-dependent tiers, missing usage and interrupted streams are not guessed.

On startup and every five minutes, the pricing worker retries retained ledger
entries marked `missing_audio_usage` or `missing_audio_cache_partition` against
their original price and usage snapshots. Successful repairs are counted as
revalued. This also works after detailed request logs have been deleted and does
not require a catalog update. Already priced amounts and account ownership are
unchanged. Entries that still need unknown data remain unpriced.
