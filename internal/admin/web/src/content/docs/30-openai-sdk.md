---
slug: openai-sdk
title: OpenAI SDK / LiteLLM
group: Clients
order: 30
intro: Any OpenAI-compatible SDK works against the gateway's `/v1` path.
---

## Python

```python
from openai import OpenAI

client = OpenAI(
    api_key="sk-cpa-•••",
    base_url="https://api.novadiffusion.com/v1",
)

resp = client.chat.completions.create(
    model="gpt-5.3-codex",
    messages=[{"role": "user", "content": "Hello"}],
)
print(resp.choices[0].message.content)
```

## Node / TypeScript

```typescript
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "sk-cpa-•••",
  baseURL: "https://api.novadiffusion.com/v1",
});

const r = await client.chat.completions.create({
  model: "gpt-5.3-codex",
  messages: [{ role: "user", content: "Hello" }],
});
console.log(r.choices[0].message.content);
```

## LiteLLM

```yaml
model_list:
  - model_name: codex
    litellm_params:
      model: openai/gpt-5.3-codex
      api_base: https://api.novadiffusion.com/v1
      api_key: sk-cpa-•••
  - model_name: claude
    litellm_params:
      model: anthropic/claude-sonnet-4-6
      api_base: https://api.novadiffusion.com
      api_key: sk-cpa-•••
```
