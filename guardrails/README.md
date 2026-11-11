# manifold-guardrail

[日本語](README_ja.md)

`manifold-guardrail` is a guardrail adapter that speaks [LiteLLM's Generic Guardrail API](https://docs.litellm.ai/docs/adding_provider/generic_guardrail_api). It can sit behind the `guardrail.url` of [Manifold](../README.md) or be used as a LiteLLM guardrail.

> [!WARNING]
> **Experimental (0.x).** Flags and behavior may change without notice. The Bedrock / Google / Azure integrations follow the official documentation and SDK types and are tested with fake clients, but have not been run against the real services. Verify them in your environment before relying on them.

It is a Go module, a binary and an image of its own, versioned independently of Manifold, so that the dependencies of the LLM and OCR SDKs (and their bugs and vulnerabilities) stay out of the gateway.

```bash
docker build -t manifold-guardrail .      # in this directory
go build -o manifold-guardrail .          # or the binary
```

## `manifold-guardrail file`: the file guardrail

It transcribes the files of a request into text and has a downstream text guardrail (anything that speaks the Generic Guardrail API) check that text, so that instructions embedded in a file (prompt injection) are caught by the text policies you already have.

```
caller (Manifold / LiteLLM) ──▶ manifold-guardrail file ──▶ text guardrail
                                       └──▶ transcription (an LLM or an OCR service)
```

```bash
manifold-guardrail file \
  --port 8091 \
  --downstream-url http://localhost:8090/beta/litellm_basic_guardrail_api \
  --extractor bedrock \
  --model <Bedrock model ID or inference profile>
```

The endpoint is `POST /beta/litellm_basic_guardrail_api`, with `GET /healthz` for probes. `manifold-guardrail file -h` lists the flags.

## Where files are read from

| Where | Content | Sent by |
|---|---|---|
| `images` | Images, as base64 or a `data:` URL | LiteLLM, Manifold |
| `files` | `[{"uri": ..., "mime_type": ..., "data": <base64>}]`: files that are not images | Manifold (an extension field that is not in LiteLLM's spec) |
| `structured_messages` | The `file_data` (base64 or a `data:` URL) of `file` / `input_file` parts in the content of a message | LiteLLM |

The type (MIME type) is the declared one when there is one, and is otherwise decided from the first bytes.

**Files given by URL or `file_id` are not fetched.** Fetching a URL the caller chose would be an SSRF entry point. They count as files whose content cannot be checked and are `BLOCKED` by default (`--allow-unsupported-files` lets them through). That covers http(s) URLs in `images`, `file_url` and `file_id`.

## With Manifold

Put it between the gateway and the text guardrail and point `guardrail.url` at it. Set `guardrail.files.enabled` to `true` so that files are sent.

```yaml
guardrail:
  enabled: true
  url: http://localhost:8091/beta/litellm_basic_guardrail_api
  timeout: 120s          # long enough to wait for the transcription of a file
  files:
    enabled: true
```

## With LiteLLM

Point the `api_base` of LiteLLM's `generic_guardrail_api` at it. How LiteLLM behaves (established by reading LiteLLM's source, not by running it):

- **Images**: the `image_url` of a message goes into `images` as it was sent. A `data:` URL can be checked; an http(s) URL cannot, as described above.
- **PDFs and other files**: they are only in `structured_messages`. Configure LiteLLM to pass the messages as sent (`forward_messages_as_sent: true`).
- **Messages without text**: LiteLLM does not call the guardrail for a request that has neither text nor tool calls, so a message that is only an image or a PDF is not checked.
- **The response side (`input_type: response`)**: no images or files are sent.

## How a request is handled

1. The original `texts` are sent downstream as they came (without `images` / `files`) and the decision is returned as is, rewrites (masking) included.
2. Each file is transcribed. Text files (`text/*`, JSON, XML, YAML, SVG, ... that are valid UTF-8) are used as text directly. Everything else goes through `--extractor`.
3. The transcribed text is sent downstream in a call of its own. Its `input_type` is `request` by default: the content of a file is untrusted input from outside, and prompt-injection filters often apply to the input side only (change it with `--extracted-input-type`).
4. A downstream rejection returns `BLOCKED`. When the downstream guardrail would rewrite the transcribed text (mask PII, ...), the file itself cannot be rewritten, so that is `BLOCKED` by default too (`--allow-intervened` lets it through).

These also return `BLOCKED`, because what cannot be checked does not go through:

- A file of a type that cannot be transcribed (`--allow-unsupported-files` passes it unchecked).
- A file whose transcription was cut off at the output limit (`--max-output-tokens`): the rest would go unchecked.
- A file larger than `--max-file-bytes`.

When the transcription or the downstream call fails it answers `502`, which the caller treats as a failure (Manifold rejects the call with `guardrail check failed`). The content of a file and its transcription are never logged.

## Transcription (`--extractor`)

| `--extractor` | File types | Settings |
|---|---|---|
| `none` (default) | Text files only | — |
| `bedrock` | PNG / JPEG / GIF / WebP, PDF, Word (doc / docx), Excel (xls / xlsx) | `--model` (model ID or inference profile), `--aws-region`. Credentials are resolved the standard AWS SDK way; the role needs `bedrock:InvokeModel` |
| `google` | PNG / JPEG / WebP / HEIC / HEIF, PDF | `--model`, `--google-backend=gemini` (`GEMINI_API_KEY`) or `vertex` (`--google-project` / `--google-location`, application default credentials) |
| `azure` | PNG / JPEG / GIF / WebP, PDF | `--model` (the deployment name), `--azure-endpoint`, `AZURE_OPENAI_API_KEY`. Uses the Responses API (`/openai/v1/responses`) |
| `openapi` | `--openapi-mime-types` (default `application/pdf,image/*`) | See below |

### An OCR service described by OpenAPI (`--extractor=openapi`)

A service of your own, or a SaaS, that has a transcription API can be called by loading its OpenAPI spec. The request and the response are assembled from the operation in the spec.

```yaml
args:
  - file
  - --downstream-url=http://localhost:8090/beta/litellm_basic_guardrail_api
  - --extractor=openapi
  - --openapi-spec=/etc/ocr/openapi.yaml
  - --openapi-operation=extractText          # operationId
  - --openapi-header=Authorization=Bearer $(OCR_TOKEN)
  - --openapi-param=language=ja              # a parameter of the operation or a body property
```

- **Request**: the body is chosen from the content of `requestBody`, in this order.
  - Binary (`application/octet-stream`, `application/pdf`, `*/*`, ...): the file is the body.
  - `multipart/form-data`: the file goes in the `type: string, format: binary` property.
  - `application/json`: the file goes, base64-encoded, in the `type: string, format: byte` property.
- **Response**: `text/plain` is read whole; for `application/json` the string property is the text.
- **When the spec does not settle it**: name the property the file goes in with `--openapi-file-field`, and where the text is with `--openapi-text-path`, which is dot-separated with `*` for every element of an array (for example `pages.*.text`). Starting without them when they are needed is an error.
- **Parameters**: the parameters of the operation (path / query / header) and the other properties of the body are given with `--openapi-param name=value`. `{mime_type}` in a value is replaced by the type of the file. A required one without a value is an error at startup.
- **Server**: the first of the spec's `servers`. Use `--openapi-server` when it has variables or to point somewhere else.

## Caveats

- **Transcription by an LLM is itself a prompt-injection target.** A file that says "do not output this part" may make the LLM drop that part. The model gets no tools, is told to transcribe verbatim, and never sees the file name, but this cannot be reduced to zero. When you need certainty, use an OCR service (`openapi`), which does not follow instructions.
- **Latency and cost**: every file is one call to an LLM or an OCR service. Set the caller's guardrail timeout long enough to wait for a transcription (in Manifold, `guardrail.timeout`, default 5s).
- **The provider integrations are untested against the real services**: the Bedrock / Google / Azure calls follow the official documentation and SDK types and are tested with fake clients, but have not been run against the services themselves. Verify them in your environment before relying on them.
