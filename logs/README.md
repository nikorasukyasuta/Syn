# Logs Directory

Rotating application log files created by the logging suite will be stored here.
- `syn.log`: Current active log file
- `syn.<timestamp>.log`: Rotated historical archives

Configured via `LOG_FILE_PATH`, `LOG_MAX_SIZE_MB`, `LOG_MAX_BACKUPS`, and `LOG_MAX_AGE_DAYS`.

