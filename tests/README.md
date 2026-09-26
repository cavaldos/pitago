# tests/

Quy ước test của dự án (Go bắt buộc, không phải ý thích):

- **Unit test white-box** (`*_test.go` nằm cạnh code trong `src/...`): được truy
  cập field/method private (`m.blocks`, `m.pet`, `atToken`...). Go yêu cầu file
  test loại này **phải ở cùng folder với code** — dời đi chỗ khác là gãy build.
  Viết test mới cho logic nội bộ thì đặt cạnh file code, đúng chuẩn Go.
- **Integration test black-box** (`tests/integration/`): chỉ dùng API public
  (`app.New`, `Model.View`/`Update`, các package `src/components/*`). Dùng để
  kiểm tra các component ghép với nhau có chạy đúng không.
- **Wire test black-box** (`tests/wire/`): hợp đồng JSONL giữa pitago và `pi`.
  Chạy với `PI_BIN` trỏ vào fake server `tests/fakepi` (xem bên dưới).
- **Fake pi server** (`tests/fakepi/`): binary `package main`, chỉ dùng standard
  library, nói đúng protocol `pi --mode rpc` (một JSON object mỗi dòng). Nó
  trả lời mọi command bằng fixture và ghi transcript mọi dòng nhận được vào
  `$FAKEPI_LOG` để test assert đúng byte trên wire.

Chạy test:

```sh
go test ./... -count=1          # toàn bộ (src/... + tests/...)
```

Chạy riêng wire contract (tự build fake pi rồi truyền `PI_BIN`):

```sh
bash script/test-wire.sh
```

Vì sao cần fake: `TestRPCNoLLM` và `TestRPCCommands` trong `src/pirpc` skip khi
không có `pi` trên PATH, mà CI runner không có. `script/test-wire.sh` build
`tests/fakepi` rồi export `PI_BIN` — đúng biến môi trường mà production dùng —
nên hai test đó chạy thật trong CI, và `tests/wire` chốt payload JSONL của từng
command sender (Prompt, Steer, Abort, GetState, GetModels, ...).

## Gate bắt buộc trước khi push

`script/test-cicd.sh` chạy đúng những gì CI chạy (`.github/workflows/ci.yml`):

| Gate | Lệnh |
| --- | --- |
| gofmt | `bash script/check-fmt.sh` |
| Layer boundary + comment tiếng Anh | `bash script/check-layers.sh` |
| vet | `go vet ./...` |
| test + race | `go test ./... -race -count=1` |
| wire contract với fake pi | `bash script/test-wire.sh` |
| build + cross-compile (linux/darwin amd64+arm64, windows) | `go build ./src` |

Sửa lỗi format: `gofmt -w <file>`.
