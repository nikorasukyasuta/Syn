# Channel Specific Filters

Place channel-specific banned words JSON files in this directory or directly inside `config/filters/`.

Filename convention: `<channel_id>.json` (e.g. `1562599674060472390.json`).

Format:
```json
[
  "channel_specific_banned_word",
  "another_filtered_phrase"
]
```
or
```json
{
  "words": [
    "channel_specific_banned_word",
    "another_filtered_phrase"
  ]
}
```

Files are loaded at startup and cached in memory. Staff can reload a specific channel's cache at runtime using:
`/filter reload channel:<channel>`

