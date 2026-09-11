# Crawler reliability dataset

`domains.txt` is intentionally small and contains public test/documentation
domains only. The `cmd/crawler-test` command accepts up to 1,000 lines from a
user-provided file, defaults to five workers, one domain stream, and a one
second delay. Use it for reliability validation, never for aggressive
scanning. Do not commit scraped personal data.
