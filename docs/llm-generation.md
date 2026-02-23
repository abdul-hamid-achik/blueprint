# LLM Generation

Blueprint is designed to be **LLM-native**. The `@>` generation slot and `bp generate` command let you describe what code should do in natural language, and an LLM fills in the implementation.

## How It Works

1. You write `@>` slots in `fn` blocks describing what the function should do
2. Run `bp generate my-service.bp` to resolve all slots via the Anthropic API
3. The LLM generates TypeScript implementations that match the function signature
4. Use `--write` to write the resolved code back to your generated output

---

## Intent Annotations (`@`)

Every block can have an `@` intent annotation describing its purpose. These serve two roles:

1. **Human documentation** -- they explain what a block does
2. **LLM context** -- they give the AI model context when generating code

```bp
@ "Authenticate via API key and enforce monthly quota"
middleware require_auth {
  before {
    |> guard header.X-API-Key -> 401 "Missing API key"
    |> key = query api_key where(key_hash == hash(header.X-API-Key)) first
    |> guard key -> 401 "Invalid API key"
    |> inject key as auth
  }
}
```

Intent annotations are included in the generated code as comments and in OpenAPI descriptions.

---

## Generation Slots (`@>`)

The `@>` arrow marks a spot where the LLM should generate an implementation. Use it inside `fn` blocks:

```bp
fn summarize {
  <- text string
  -> string

  @> Summarize the given text in 2-3 sentences.
     Use clear, professional language.
     Preserve key facts and numbers.
}
```

The `@>` block is free-form text -- write a clear prompt describing the desired behavior.

### Guidelines for Good Generation Prompts

**Be specific about inputs and outputs:**

```bp
fn calculate_price {
  <- plan       string
  <- operations int
  -> money

  @> Calculate the price based on plan tier:
     - free: always $0
     - pro: $0.01 per operation
     - enterprise: $0.005 per operation with $50 minimum
     Return the total as a decimal number.
}
```

**Include edge cases:**

```bp
fn parse_csv {
  <- content string
  -> json

  @> Parse the CSV content into an array of objects.
     The first row contains headers.
     Handle quoted fields with commas inside them.
     Return empty array if content is empty.
     Trim whitespace from all values.
}
```

**Reference the function signature:**

The LLM receives the full function signature (inputs, output type) along with your prompt, so it knows the expected types.

---

## Using `bp generate`

### Prerequisites

Set your Anthropic API key:

```bash
export ANTHROPIC_API_KEY=sk-ant-api03-...
```

### Preview Mode (default)

```bash
bp generate my-service.bp
```

Prints the resolved implementations to stdout without modifying any files. Use this to review what the LLM generates before committing.

### Write Mode

```bash
bp generate my-service.bp --write
```

Writes the resolved code back to the generated output files. The implementation replaces the `@>` slot content in the corresponding `src/functions/<name>.ts` file.

### What Gets Generated

For a function like:

```bp
fn watermark {
  <- file     image/*
  <- text     string
  <- position string
  <- opacity  float
  -> file image/*

  @> Apply a text watermark to the image at the given position
     with the specified opacity. Use sharp or canvas.
}
```

The LLM generates a TypeScript implementation in `src/functions/watermark-impl.ts`:

```typescript
import sharp from 'sharp';

export async function apply(
  file: Buffer,
  text: string,
  position: string,
  opacity: number,
): Promise<Buffer> {
  // LLM-generated implementation here
}
```

The wrapper file `src/functions/watermark.ts` imports and calls this implementation.

---

## Combining `@` and `@>`

Use `@` for documentation and `@>` for generation together:

```bp
@ "Convert currency using live exchange rates"
fn convert_currency {
  <- amount   money
  <- from     string
  <- to       string
  -> money

  @> Fetch the current exchange rate from a free API
     (e.g., exchangerate-api.com) and convert the amount.
     Handle API errors gracefully.
     Round the result to 2 decimal places.
}
```

The `@` annotation appears in OpenAPI docs and generated comments. The `@>` slot tells the LLM how to implement it.

---

## Best Practices

### 1. Keep Functions Focused

One function, one job. Don't ask the LLM to generate a function that does 5 things.

```bp
# Good: focused function
fn resize_image {
  <- file   image/*
  <- width  int
  <- height int
  -> file image/*

  @> Resize the image to the target dimensions using sharp.
     Maintain aspect ratio using "cover" fit.
}

# Bad: too many responsibilities
fn process_image {
  <- file image/*
  -> json

  @> Resize, watermark, compress, upload to S3, generate thumbnail,
     and return metadata for all versions.
}
```

### 2. Specify Libraries When Relevant

```bp
fn generate_pdf {
  <- html string
  -> file application/pdf

  @> Convert the HTML to a PDF using puppeteer.
     Use A4 paper size with 1cm margins.
}
```

### 3. Define Error Behavior

```bp
fn validate_address {
  <- address json
  -> json

  @> Validate the address using a geocoding API.
     If the address is invalid, throw an error with message "Invalid address".
     Return the normalized address with lat/lng coordinates.
}
```

### 4. Use `impl` for Deterministic Logic

If the logic is straightforward and doesn't need AI:

```bp
# Use impl for deterministic code
fn hash_password {
  <- password string
  -> string

  impl node {
    module: "./internal/auth"
    func:   "hashPassword"
  }
}

# Use @> for complex/creative logic
fn generate_summary {
  <- text string
  -> string

  @> Summarize the text in 2-3 clear sentences.
}
```

---

## LLM-Native Workflow

A typical workflow combining Blueprint's LLM features:

```bash
# 1. Write your service with @> slots for complex functions
vim my-service.bp

# 2. Check for syntax/semantic errors
bp check my-service.bp

# 3. Build the base project
bp build my-service.bp

# 4. Generate implementations for @> slots
bp generate my-service.bp --write

# 5. Run and test
bp test my-service.bp
bp run my-service.bp
```

This workflow lets you declare the **what** (Blueprint syntax) and let the LLM fill in the **how** (TypeScript implementations).
