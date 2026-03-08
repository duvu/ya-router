# Huong Dan Su Dung github-copilot-svcs

Tai lieu nay danh cho nguoi dung van hanh va tich hop `github-copilot-svcs`.

## github-copilot-svcs la gi

Day la mot proxy OpenAI-compatible de client co the goi cac endpoint quen thuoc nhu:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/embeddings`

Phia sau, service se route request toi:

- GitHub Copilot
- OpenAI Codex

theo cau hinh va model duoc request.

## Cach dung nhanh

### 1. Build binary

```bash
make build
```

### 2. Tao file config runtime

```bash
mkdir -p ~/.local/share/github-copilot-svcs
cp config.example.json ~/.local/share/github-copilot-svcs/config.json
```

### 3. Chon provider can bat

Mo file config va bat:

- `providers.copilot.enabled: true` neu dung GitHub Copilot
- `providers.codex.enabled: true` neu dung Codex

### 4. Xac thuc

Copilot:

```bash
./github-copilot-svcs auth copilot
```

Codex voi API key:

```bash
export OPENAI_API_KEY=your_api_key
./github-copilot-svcs auth codex --api-key
```

Codex voi device code (OAuth flow):

```bash
./github-copilot-svcs auth codex
```

Lenh nay chay OpenAI OAuth device-code flow truc tiep (tuong tu Copilot):

- Hien thi URL va code de nguoi dung xac thuc tren trinh duyet
- Sau khi xac thuc, token duoc luu vao `~/.local/share/github-copilot-svcs/config.json`
- Token bao gom `access_token`, `refresh_token` va `expires_at`
- Khi token het han, service tu dong refresh bang `refresh_token`
- Khong can cai dat `codex` CLI rieng

### 5. Chay service

```bash
./github-copilot-svcs run
```

### 6. Kiem tra

```bash
curl http://localhost:7071/health
curl http://localhost:7071/v1/models
```

## Cac lenh thuong dung

```bash
./github-copilot-svcs status
./github-copilot-svcs config
./github-copilot-svcs models
./github-copilot-svcs models --provider copilot
./github-copilot-svcs models --provider codex
./github-copilot-svcs refresh --provider copilot
./github-copilot-svcs refresh --provider codex
```

## Logic route model

Service route theo thu tu:

1. `routing.model_map`
2. Danh sach model ma provider tra ve
3. `routing.default_provider`

Neu request khong co field `model`, service dung `routing.default_model`.

Luu y:

- `default_model` khong con la co che ep moi request phai dung cung mot model.
- No chi la gia tri mac dinh khi client khong gui `model`.

## Mau config don gian

```json
{
  "port": 7071,
  "config_version": 1,
  "routing": {
    "default_model": "gpt-5-mini",
    "default_provider": "copilot",
    "show_unavailable_models": false,
    "model_map": {
      "text-embedding-3-large": {
        "provider": "codex"
      }
    }
  },
  "providers": {
    "copilot": {
      "enabled": true,
      "auth": {
        "mode": "device_code"
      },
      "allowed_models": []
    },
    "codex": {
      "enabled": true,
      "auth": {
        "mode": "device_code",
        "api_key_env": "OPENAI_API_KEY"
      },
      "allowed_models": []
    }
  }
}
```

## `allowed_models` hoat dong the nao

- `allowed_models: []` nghia la cho phep tat ca model provider do discover duoc.
- Neu dat danh sach cu the, service chi hien thi model nam trong danh sach do cho provider tuong ung.

## Bao mat

- Khong commit file config runtime co token that.
- Khong log gia tri token.
- Moi credential (Copilot va Codex) deu luu trong `~/.local/share/github-copilot-svcs/config.json`.
- `api_key` phu hop hon cho moi truong server.

## Loi thuong gap

### `auth codex` fail

- Kiem tra `providers.codex.auth.mode` (phai la `device_code` hoac `api_key`).
- Neu dung `api_key`, kiem tra env var co ton tai trong shell dang chay binary.
- Neu dung `device_code`, chay lai `./github-copilot-svcs auth codex` de xac thuc lai.
- Kiem tra ket noi internet toi `auth0.openai.com`.

### `models` khong thay model mong muon

- Kiem tra provider da bat chua.
- Kiem tra `status` de biet provider da auth thanh cong chua.
- Kiem tra `allowed_models` co dang loc mat model khong.

### Request di sai provider

- Them rule vao `routing.model_map`.

## Tich hop voi client OpenAI-compatible

Tro `base_url` cua client ve:

```text
http://localhost:7071/v1
```

Nhieu SDK van can `api_key`, nhung gia tri nay thuong co the de gia vi proxy nay khong su dung `Authorization` theo cach cua OpenAI goc.
