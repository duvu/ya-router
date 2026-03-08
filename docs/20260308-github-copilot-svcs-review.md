# Review github-copilot-svcs

Ngày review: 2026-03-08

## 1. Mục tiêu và phạm vi

Tài liệu này review repository `github-copilot-svcs` theo góc nhìn vận hành và code review:

- Kiến trúc và luồng xử lý chính
- Cách cấu hình, chạy và kiểm thử
- Các điểm mạnh hiện có
- Các lỗi/rủi ro quan trọng cần ưu tiên sửa
- Gợi ý lộ trình cải thiện

Phạm vi đọc mã nguồn tập trung vào:

- `src/main.go`
- `src/cli.go`
- `src/config.go`
- `src/auth.go`
- `src/proxy.go`
- `src/models.go`
- `src/transform.go`
- `src/embeddings.go`
- `README.md`
- `Dockerfile`
- `docker-compose.yml`
- bộ test trong `src/*_test.go`

## 2. Tóm tắt điều hành

Đây là một service Go tương đối gọn, mục tiêu rõ ràng: làm reverse proxy cho GitHub Copilot và lộ ra các endpoint gần tương thích OpenAI như `/v1/chat/completions`, `/v1/embeddings`, `/v1/models`.

Điểm tốt:

- Cấu trúc code đơn giản, dễ lần luồng
- Có build, test, race test và docker hóa cơ bản
- Có xử lý auth device flow, refresh token, retry, health check
- Có ý thức về production concerns như timeouts, circuit breaker, worker pool, pprof

Tuy nhiên, phần reliability hiện còn một số lỗi hành vi nghiêm trọng:

- Request coalescing cho `/v1/models` bị sai semantics và có thể trả về `nil` hoặc panic khi concurrent
- `merge` migration đang ghi đè config tùy chỉnh của người dùng dù tài liệu nói là preserve
- Retry helper làm mất `context`, khiến `proxy_context` không thực sự được propagate xuống outbound request
- Khi upstream lỗi ở lần retry cuối, body response có thể đã bị đóng trước khi trả về caller
- `allowed_models` được document là có hiệu lực nhưng thực tế chưa được áp dụng cho model listing

Kết luận ngắn: repo có nền tảng tốt để vận hành nội bộ, nhưng chưa đủ chắc cho production load/concurrency nếu chưa xử lý các lỗi ở mục 6.

## 3. Kiến trúc hiện tại

### 3.1 Entry points

- `src/main.go:12-88`
  - CLI dispatcher cho các command `run`, `auth`, `status`, `config`, `models`, `migrate-config`, `refresh`, `version`
- `src/cli.go:127-195`
  - `handleRunWithMigration()` là điểm khởi động server

### 3.2 Các thành phần chính

- `src/config.go`
  - Load/save config tại `~/.local/share/github-copilot-svcs/config.json`
  - Default values cho port, models, timeouts
  - Hỗ trợ migrate config

- `src/auth.go`
  - OAuth device flow với GitHub
  - Exchange GitHub token sang Copilot token
  - Refresh token với exponential backoff

- `src/proxy.go`
  - HTTP client dùng chung
  - Timeout init
  - Retry outbound request
  - Circuit breaker
  - Worker pool
  - Proxy logic cho chat/embeddings

- `src/models.go`
  - Fetch model list từ GitHub Copilot
  - Cache 24h
  - Models endpoint `/v1/models`

- `src/transform.go`
  - Enforce `default_model`

- `src/embeddings.go`
  - Normalize request/response embeddings cho tương thích OpenAI hơn

### 3.3 HTTP surface

Theo `src/cli.go:153-167`, server hiện expose:

- `GET /v1/models`
- `POST /v1/embeddings`
- `POST /v1/chat/completions`
- `GET /health`
- `GET /debug/pprof/*`

### 3.4 Luồng request chat

Luồng thực thi chính:

1. Request vào `proxyHandler()` tại `src/proxy.go:375-429`
2. Tạo `context.WithTimeout()` theo `proxy_context`
3. Submit job vào `globalWorkerPool`
4. `processProxyRequest()` tại `src/proxy.go:451-620`
5. `ensureValidToken()` kiểm tra/refresh token
6. Đọc body, transform model nếu là chat
7. Map path OpenAI-like sang Copilot upstream
8. Gọi upstream qua `makeRequestWithRetry()`
9. Stream hoặc copy response về client

## 4. Cấu hình và vận hành

### 4.1 Config

Config hiện có các nhóm field chính:

- Network/server: `port`
- Auth/token: `github_token`, `copilot_token`, `expires_at`, `refresh_in`
- Model policy: `allowed_models`, `default_model`
- Timeout policy: `timeouts.*`

Defaults hiện nằm ở hai nơi:

- `src/config.go:106-150`
- `config.example.json:1-21`

Điều này giúp có template rõ ràng, nhưng cũng tạo nguy cơ drift nếu hai nguồn mặc định lệch nhau.

### 4.2 Docker

Docker build/runtime tương đối sạch:

- Multi-stage build tại `Dockerfile:1-38`
- Runtime chạy non-root `appuser`
- Mount config directory qua `docker-compose.yml:11-14`

Lưu ý:

- `docker-compose.yml:13-14` set biến môi trường `PORT=7071`
- Code hiện không đọc env `PORT`; service lấy port từ file config (`src/config.go:71-103`, `src/cli.go:169-180`)

### 4.3 Auth state hiện được lưu ở đâu

Điểm cần làm rõ: repository này hiện **không dùng** hoặc parse `auth.json` của GitHub Copilot extension/IDE.

Thay vào đó, auth state được lưu chung trong file config runtime:

- Local Linux/user runtime: `~/.local/share/github-copilot-svcs/config.json`
- Trong container: `/home/appuser/.local/share/github-copilot-svcs/config.json`
- Docker compose hiện mount `./config` của repo vào path trên trong container (`docker-compose.yml:11-12`)

Path này được tính trong:

- `src/config.go:47-68`

Các field auth được persist trong cùng file JSON:

- `github_token`
- `copilot_token`
- `expires_at`
- `refresh_in`

Điểm này khác với một số implementation/extension GitHub Copilot khác có thể dùng `auth.json` riêng. Với repo này, nguồn sự thật hiện tại là `config.json`, không phải `auth.json`.

### 4.4 Parsing auth/config hiện tại

Struct parse chính nằm tại `src/config.go:20-44`:

- `GitHubToken string  json:"github_token"`
- `CopilotToken string json:"copilot_token"`
- `ExpiresAt int64     json:"expires_at"`
- `RefreshIn int64     json:"refresh_in"`

Luồng parse:

1. `loadConfig()` lấy path bằng `getConfigPath()`
2. Mở file bằng `os.Open(...)`
3. Parse JSON bằng `json.NewDecoder(file).Decode(&cfg)`
4. Bổ sung default cho `port`, `allowed_models`, `default_model`, `timeouts`

Luồng ghi:

1. `saveConfig()` tạo file tạm `config.json.tmp`
2. Encode JSON bằng `json.NewEncoder(f).Encode(cfg)`
3. `fsync`
4. `rename` atomically về `config.json`

Tức là auth parsing hiện rất đơn giản:

- Không có encryption
- Không có schema version riêng cho auth
- Không có file auth tách biệt
- Không có parser riêng cho token blob của GitHub extension

### 4.5 Cơ chế login hiện tại

Luồng login hiện tại nằm ở `src/auth.go:50-110` và là **GitHub OAuth Device Flow**, sau đó đổi sang Copilot token.

Các bước thực tế:

1. Người dùng chạy `./github-copilot-svcs auth`
2. Service gọi `POST https://github.com/login/device/code`
   - dùng `client_id = Iv1.b507a08c87ecfe98`
   - dùng `scope = read:user`
3. GitHub trả về:
   - `device_code`
   - `user_code`
   - `verification_uri`
   - `expires_in`
   - `interval`
4. Service in `verification_uri` và `user_code` ra terminal để người dùng mở trình duyệt và xác nhận
5. Service poll `POST https://github.com/login/oauth/access_token` cho tới khi nhận `access_token`
6. `access_token` đó được lưu thành `github_token`
7. Service dùng `github_token` gọi `GET https://api.github.com/copilot_internal/v2/token`
8. GitHub trả về:
   - `token`
   - `expires_at`
   - `refresh_in`
9. Service lưu các giá trị này thành:
   - `copilot_token`
   - `expires_at`
   - `refresh_in`
10. Toàn bộ state được ghi xuống `config.json`

Các struct parse response trong auth flow:

- `deviceCodeResponse` tại `src/auth.go:27-33`
- `tokenResponse` tại `src/auth.go:35-39`
- `copilotTokenResponse` tại `src/auth.go:41-48`

### 4.6 Cơ chế refresh sau login

Trong runtime, service không bắt người dùng login lại cho mọi request.

Luồng hiện tại:

1. Trước request proxy, `ensureValidToken()` chạy tại `src/proxy.go:269-303`
2. Nếu `copilot_token` chưa có, service gọi lại full `authenticate()`
3. Nếu token sắp hết hạn, service gọi `refreshToken()`
4. `refreshToken()` dùng `github_token` đã lưu để xin một `copilot_token` mới qua `GET https://api.github.com/copilot_internal/v2/token`
5. Nếu refresh thất bại sau nhiều lần retry, service mới fallback về full device-flow login lại

Nói ngắn gọn:

- Login lần đầu: browser/device flow
- Runtime bình thường: refresh bằng `github_token`
- Chỉ khi refresh fail mới cần re-auth

## 5. Kiểm chứng đã chạy

Các lệnh đã chạy trong lúc review:

- `go build -o /tmp/github-copilot-svcs-review ./src`
- `go test ./src/...`
- `go test -race ./src/...`
- `go vet ./src/...`

Kết quả:

- Build pass
- Unit/integration test hiện tại pass
- Race test pass
- Vet không báo lỗi

Nhận xét:

- Việc các lệnh trên pass không phủ định các lỗi ở mục 6
- Các lỗi quan trọng hiện nằm ở logic concurrency/cancellation và chưa có test chuyên biệt để kích hoạt

## 6. Findings

### F1. High: request coalescing cho `/v1/models` đang broadcast sai, concurrent waiter có thể nhận `nil`

File liên quan:

- `src/proxy.go:236-263`
- `src/models.go:136-166`

Vấn đề:

- `CoalesceRequest()` tạo `chan interface{}` buffer 1
- Sau khi `fn()` xong, code chỉ `ch <- result` một lần rồi `close(ch)`
- Mọi waiter khác đang block trên `<-ch` sẽ không cùng nhận `result`; chỉ một waiter nhận giá trị thật
- Các waiter còn lại sẽ đọc zero-value từ channel đã đóng, tức `nil`
- `modelsHandler()` sau đó ép kiểu `result.(*ModelList)` tại `src/models.go:165`, có thể panic khi `result == nil`

Tác động:

- Dưới tải concurrent vào `/v1/models`, endpoint có thể panic ngẫu nhiên
- Đây là lỗi correctness thực sự, không chỉ là tối ưu hóa chưa hoàn thiện

Khuyến nghị:

- Không dùng một channel một phần tử để “broadcast”
- Chuyển sang mô hình `sync.Cond`, `singleflight.Group`, hoặc phát kết quả qua struct shared state rồi mọi waiter đọc cùng một object

### F2. High: `merge` migration đang ghi đè config tùy chỉnh của người dùng ở mỗi lần startup

File liên quan:

- `src/main.go:29-42`
- `src/config.go:218-241`
- `README.md:333-356`

Vấn đề:

- `run` mặc định chạy với `--config-migrate merge`
- `mergeConfigs()` chỉ preserve:
  - `github_token`
  - `copilot_token`
  - `expires_at`
  - `refresh_in`
  - `port` nếu khác `7071`
- Các field tùy chỉnh khác như:
  - `default_model`
  - `allowed_models`
  - `timeouts.*`
  sẽ bị thay bằng defaults

Tác động:

- Người dùng đổi model mặc định sang `claude-*` hoặc tune timeout sẽ bị mất cấu hình sau restart
- Hành vi này trái với README, nơi `merge` được mô tả là preserve “custom settings”
- Vì migration chạy mặc định lúc startup, lỗi này ảnh hưởng trực tiếp đến production behavior

Khuyến nghị:

- `merge` phải preserve toàn bộ field người dùng đã set, chỉ bổ sung field mới còn thiếu
- Nếu muốn reset policy/model/timeouts về default, cần để đó là `override`, không phải `merge`

### F3. High: retry helper làm mất `context`, nên `proxy_context` không thực sự áp vào outbound request

File liên quan:

- `src/proxy.go:378-379`
- `src/proxy.go:503-525`
- `src/proxy.go:321-333`
- `README.md:238`
- `README.md:369`

Vấn đề:

- `processProxyRequest()` tạo request bằng `http.NewRequestWithContext(ctx, ...)`
- Nhưng `makeRequestWithRetry()` không dùng request đó trực tiếp
- Mỗi attempt lại tạo request mới bằng `http.NewRequest(...)` không gắn context

Tác động:

- Timeout/cancel từ `proxy_context` không được propagate xuống HTTP call thực tế
- Outbound request có thể tiếp tục chạy đến khi `sharedHTTPClient.Timeout` hết hạn
- Điều này làm cho tuyên bố “proxy_context timeout” trong code/docs không đúng về mặt hành vi

Hệ quả phụ:

- `proxyHandler()` có thể vào nhánh timeout ở `src/proxy.go:421-426` trong khi worker vẫn còn tiếp tục làm việc
- Đây là nền cho lỗi double-write/racing write lên `ResponseWriter`

Khuyến nghị:

- Retry phải clone request nhưng giữ nguyên `ctx`
- Có thể dùng `req.Clone(ctx)` hoặc `http.NewRequestWithContext(ctx, ...)` cho mọi attempt

### F4. High: ở lần retry cuối với status retriable, response body bị đóng trước khi trả về caller

File liên quan:

- `src/proxy.go:353-365`
- `src/proxy.go:531-620`

Vấn đề:

- Khi upstream trả về status retriable như `429` hoặc `5xx`, code đóng `resp.Body` ở `src/proxy.go:359-360`
- Nếu đó là `attempt == maxChatRetries`, code vẫn `return resp, nil` tại `src/proxy.go:362-365`
- Caller sau đó vẫn cố `io.ReadAll(resp.Body)` hoặc `io.Copy(...)`

Tác động:

- Client có thể nhận empty body, lỗi đọc stream, hoặc mất payload lỗi hữu ích từ upstream
- Điều này làm quan sát lỗi thực tế khó hơn và có thể phá contract response

Khuyến nghị:

- Chỉ đóng body khi chắc chắn sẽ retry tiếp
- Ở lần cuối, trả nguyên response chưa đóng

### F5. Medium: `allowed_models` được document là có hiệu lực nhưng thực tế chưa được áp dụng vào model listing

File liên quan:

- `src/models.go:56-85`
- `src/models.go:131-166`
- `src/cli.go:212-236`
- `README.md:266-268`

Vấn đề:

- Hàm `filterAllowedModels()` có tồn tại nhưng không được gọi
- `/v1/models` trả toàn bộ danh sách upstream hoặc fallback
- CLI `models` cũng in toàn bộ `models.Data`

Tác động:

- README mô tả `allowed_models` sẽ control output của `GET /v1/models`, nhưng behavior hiện tại không đúng
- Client có thể nhìn thấy model mà operator tưởng đã giới hạn

Khuyến nghị:

- Áp `filterAllowedModels()` trước khi cache hoặc trước khi encode response
- Làm tương tự cho lệnh CLI `models`

### F6. Medium: pprof đang mở công khai trên cùng listener, không auth và không config gate

File liên quan:

- `src/cli.go:162-167`
- `docker-compose.yml:9-10`
- `README.md:211-217`

Vấn đề:

- `/debug/pprof/*` luôn được mount
- Docker compose publish trực tiếp port service ra host
- Không có auth, IP allowlist, hoặc cờ config để tắt

Tác động:

- Nếu service bị expose ra mạng nội bộ rộng hoặc internet, pprof có thể leak heap, goroutine, trace và hỗ trợ profiling từ xa
- Đây là attack surface không cần thiết cho mặc định production

Khuyến nghị:

- Tắt theo mặc định
- Chỉ bật qua config/flag riêng cho debug
- Nếu cần, bind sang listener nội bộ khác

## 7. Các điểm yếu bổ sung nhưng chưa xếp mức cao

- `README.md:257-286` chứa một khối `json` không hợp lệ vì chèn bullet markdown vào giữa object JSON
- `src/models.go:131-163` không refresh token trước khi gọi upstream, nên `/v1/models` có thể rơi về fallback khi token hết hạn
- `src/auth.go:113-150` comment nói “Poll for 2 minutes max” nhưng code lặp `120 * interval`, không dùng `expires_in`
- `src/integration_test.go:12-139` tạo `httptest.NewServer()` nhưng không inject `mockServer.URL` vào luồng proxy thực tế; test này thiên về transformation hơn là integration end-to-end

## 8. Điểm mạnh

- Mã nguồn nhỏ, dễ hiểu, không bị framework overhead
- Auth flow và token refresh khá rõ ràng
- Embeddings compatibility layer đơn giản, dễ maintain
- Có test cho config migration, model enforcement và embeddings normalization
- Có chạy được `go test -race`, đây là điểm tốt cho repo Go service

## 9. Ưu tiên xử lý đề xuất

### P0

- Sửa `CoalesceRequest()` hoặc thay bằng `singleflight.Group`
- Sửa `makeRequestWithRetry()` để giữ nguyên `context`
- Sửa logic đóng `resp.Body` ở lần retry cuối

### P1

- Sửa semantics của `mergeConfigs()` để thực sự preserve config tùy chỉnh
- Áp dụng `allowed_models` đúng vào HTTP và CLI listing
- Tắt pprof mặc định hoặc đưa sau config flag

### P2

- Bổ sung test concurrent cho `/v1/models`
- Bổ sung test timeout/cancel cho proxy path
- Bổ sung test retry với upstream `429/5xx` để đảm bảo body còn nguyên
- Dọn lại README và làm rõ behavior của migration/model policy

## 10. Requirement mở rộng: support cả Codex

### 10.1 Mục tiêu

Mở rộng `github-copilot-svcs` từ mô hình single-provider hiện tại thành service hỗ trợ **cả GitHub Copilot và Codex**, trong khi vẫn giữ backward compatibility cho luồng đang chạy với GitHub Copilot.

### 10.2 Giả định làm việc cho requirement này

Để tránh mơ hồ, tài liệu này chốt theo giả định sau:

- “Support cả Codex” nghĩa là service phải hỗ trợ thêm một provider/upstream Codex bên cạnh GitHub Copilot
- GitHub Copilot vẫn là provider hiện có và không bị phá behavior hiện tại
- Codex có thể có auth mechanism khác GitHub Copilot
- Vì code hiện tại hard-code toàn bộ luồng GitHub Copilot, support Codex không nên làm kiểu vá thêm if/else cục bộ; cần tách abstraction provider

Nếu sau này xác nhận Codex upstream cụ thể khác với giả định này, section requirement cần được cập nhật lại theo upstream/auth contract thực tế.

### 10.3 Functional requirements

1. Service phải hỗ trợ ít nhất 2 provider:
   - GitHub Copilot
   - Codex

2. Phải có cách chọn provider cho từng request hoặc từng deployment, ví dụ:
   - default provider ở config
   - override theo request header/path/config mapping

3. Service phải tiếp tục expose các OpenAI-compatible endpoints hiện có:
   - `/v1/chat/completions`
   - `/v1/embeddings`
   - `/v1/models`

4. `GET /v1/models` phải trả đúng danh sách model theo provider đang chọn, thay vì hard-code Copilot-only như hiện tại.

5. Auth state phải tách theo provider. Tối thiểu cần tránh việc dùng chung một cặp field `github_token` / `copilot_token` cho mọi upstream.

6. CLI phải hỗ trợ quản lý auth theo provider:
   - `auth`
   - `status`
   - `refresh`
   - `models`

7. Migration từ config cũ phải an toàn:
   - config GitHub Copilot cũ vẫn chạy được
   - field mới cho Codex phải được thêm mà không làm mất token hiện hữu

### 10.4 Kiến trúc đề xuất ở mức requirement

Do code hiện tại hard-code Copilot URL, headers và auth flow trong:

- `src/auth.go`
- `src/models.go`
- `src/proxy.go`

Requirement kỹ thuật nên là:

- Tách abstraction `Provider`
- Mỗi provider chịu trách nhiệm cho:
  - auth/login
  - refresh token
  - build upstream headers
  - model discovery
  - path mapping nếu khác nhau
  - request/response normalization nếu cần

Một hướng thiết kế hợp lý:

- `providers/copilot`
- `providers/codex`
- `ProviderRegistry`
- `ProviderConfig`
- `AuthStore` tách per-provider

### 10.5 Cấu hình đề xuất

Config hiện tại đang phẳng và Copilot-specific. Requirement mới nên chuyển về dạng provider-aware, ví dụ:

```json
{
  "default_provider": "copilot",
  "providers": {
    "copilot": {
      "enabled": true,
      "default_model": "gpt-5-mini"
    },
    "codex": {
      "enabled": true,
      "default_model": "codex",
      "api_key": "..."
    }
  }
}
```

Lưu ý:

- Đây là ví dụ requirement-level, không phải schema chốt cuối cùng
- Điểm bắt buộc là config phải tách provider state, không tiếp tục hard-code một schema chỉ hợp với Copilot

### 10.6 Auth requirement cho Codex

GitHub Copilot hiện dùng device flow. Codex có thể dùng cơ chế auth khác.

Vì vậy requirement cần ghi rõ:

- Không được giả định mọi provider đều dùng device flow
- Auth API phải được provider hóa
- Storage phải phân tách credential theo provider
- `auth/status/refresh` cần chỉ rõ đang thao tác trên provider nào

### 10.7 Non-functional requirements

- Không phá backward compatibility với deployment Copilot hiện tại
- Không đưa credential của provider này đè lên provider khác
- Có test cho:
  - config migration cũ -> mới
  - auth state tách theo provider
  - models endpoint theo provider
  - request routing theo provider
- Tài liệu vận hành phải chỉ rõ:
  - provider mặc định
  - cách auth từng provider
  - cách chọn provider cho request

### 10.8 Ảnh hưởng tới review hiện tại

Requirement support Codex làm rõ một hạn chế kiến trúc quan trọng của code hiện nay:

- Codebase hiện gắn chặt vào GitHub Copilot ở mức endpoint, headers, token model và config schema
- Vì vậy nếu thực sự muốn support Codex, refactor quan trọng nhất không phải thêm model name, mà là tách `provider abstraction`

Nói cách khác, đây không còn là thay đổi “thêm 1 model”; đây là thay đổi từ single-provider proxy sang multi-provider gateway.

## 11. Kết luận

`github-copilot-svcs` là một service có mục tiêu rõ và khá gần trạng thái “dùng được”, đặc biệt cho môi trường nội bộ hoặc luồng vận hành đơn giản. Tuy nhiên, trước khi coi là ổn cho production traffic thật, cần xử lý ngay các lỗi về coalescing, migration semantics, retry/context và error response handling. Đây là các lỗi ảnh hưởng trực tiếp đến correctness chứ không chỉ là polish.
