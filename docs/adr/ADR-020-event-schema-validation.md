# ADR-020: Event and artifact schema validation

## Status
Accepted

## Context
Prior to this segment, `POST /events` and `POST /artifacts` accepted any valid JSON body without verifying that the payload conforms to the expectations of downstream consumers. Malformed or incomplete events reached the store and were delivered to webhook subscribers, causing silent data quality failures.

Sites vary in what fields they require. A central hardcoded schema would be too restrictive; no validation at all shifts the burden entirely to consumers.

## Decision
Each site can declare optional required-field schemas in `contracts/sites/<site_id>/config.yaml`:

```yaml
event_schema:
  required:
    - type
    - source

artifact_schema:
  required:
    - name
```

When a schema is configured:

1. After reading and parsing the JSON body, the handler checks that every field listed in `required` is present as a top-level JSON key.
2. If any required fields are absent, the handler returns **422 Unprocessable Entity** with a JSON body:

```json
{
  "error": "schema validation failed",
  "missing_fields": ["source"]
}
```

3. If all required fields are present (regardless of their values), the handler proceeds normally and returns 201.
4. When no schema is configured (`EventSchema`/`ArtifactSchema` is `nil`), no validation is performed and the existing 201/400 behavior is unchanged.

### Implementation
- `config.SchemaConfig{Required []string}` added to `config.go`.
- `SiteConfig.EventSchema` and `SiteConfig.ArtifactSchema` (`*SchemaConfig`) added; `nil` disables validation.
- `SiteContext.Config config.SiteConfig` added so handlers can access the full resolved config without a second `config.Load()` call.
- `SiteMiddleware` populates `sc.Config`.
- `validateJSONFields(body, required)` helper in `handlers.go` does the check.
- `PostEventHandler` and `PostArtifactHandler` call the helper.

## Consequences
- Sites that do not configure schemas are entirely unaffected.
- Required-field validation is intentionally shallow (top-level key presence only); nested or type-based constraints are out of scope for this segment.
- The `SiteContext.Config` field provides handlers a zero-cost access path to the site configuration, enabling future segments to add further per-site behaviour without additional middleware changes.
