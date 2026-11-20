# Third-party notice: lm-detector

Model fingerprint scoring in this repository uses the MIT-licensed
[lm-detector](https://github.com/Ikaleio/lm-detector) Go SDK and its reference
bank, unchanged.

- Module: `github.com/Ikaleio/lm-detector/sdk/go`
- Pinned version: `v0.2.0` (tag `sdk/go/v0.2.0`), MIT
- Used in: `pkg/lmfp`, `service/channel_fingerprint*.go`,
  `controller/channel_fingerprint*.go`

The license text below must stay with any copy or substantial portion of the
software.

```
MIT License

Copyright (c) 2026 xqy2006

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Reference data ships with the binary

`pkg/lmfp/data/` holds one published reference bank release:

| File | SHA-256 (pinned in `pkg/lmfp/lmfp.go`) | Size |
| --- | --- | --- |
| `unified_bank.json` (`bundle.json`, 75 models `built_at` 2026-11-19) | `85c7887bff9c1fd066666c23733ec259facc8f62d3f86a89d1795d088b6eccd6` | 8.9 KB |
| `shared_detector.bin` | `a55d4bb275b09b630d9deeeac5a119077869a40dc65132ffff0ab87e58b7c9e9` | 5.4 MB |

Both files are embedded with `//go:embed`, so a release build scores without any
download and without operator setup. The gateway never contacts `lm.ikale.io`,
its jsDelivr mirror, or any other fingerprint host at runtime.

`pkg/lmfp.Load` verifies both digests against the compiled-in pins before the SDK
reads them; a mismatch fails closed. An operator may point at a newer published
bank instead:

| Variable | Meaning |
| --- | --- |
| `FINGERPRINT_DATA_DIR` | Directory holding `unified_bank.json` and `shared_detector.bin` |
| `FINGERPRINT_BANK_SHA256` | Required with `FINGERPRINT_DATA_DIR`: digest of `unified_bank.json` |
| `FINGERPRINT_DETECTOR_SHA256` | Required with `FINGERPRINT_DATA_DIR`: digest of `shared_detector.bin` |
| `FINGERPRINT_TOKEN_BUDGET` | Optional default estimated input and output token budget for one check (default 24000) |
| `FINGERPRINT_PROBE_TIMEOUT_SECONDS` | Optional per-request timeout (default 120) |

Every result reports the verified bank and detector digests and the bank build
time it was scored against.

## What a result does and does not mean

Scoring ranks a channel's replies against the models present in the reference
bank. A closest match is a statistical comparison inside that bank: it is not
proof of model identity, quality, or authenticity, and a model outside the bank
still receives a closest candidate. Both notices are returned with every result
and are displayed next to it.
