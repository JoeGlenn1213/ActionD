# Java Checkstyle Plugin

Performs code style checking on Java projects using Checkstyle.

## Features

- ✅ Automatic Checkstyle JAR download and caching
- ✅ Google Java Style configuration
- ✅ JSON and HTML report generation
- ✅ Detailed violation metrics
- ✅ Severity-based categorization

## Triggers

- `git.push` - Check code style on every push

## Configuration

Uses Google Java Style by default (`config/google_checks.xml`).

## Output

### Artifacts
- `checkstyle-report.json` - Machine-readable report
- `checkstyle-report.html` - Human-readable HTML report

### Metrics
```json
{
  "total_violations": 15,
  "files_with_violations": 8,
  "severity": {
    "error": 3,
    "warning": 10,
    "info": 2
  }
}
```

## Requirements

- Python 3.7+
- Java 8+ (for running Checkstyle)

## Example Usage

```bash
# Manual test
echo '{"event":{"type":"git.push"},"repo_path":"/path/to/java/project"}' | python3 run.py
```

## Checkstyle Version

Currently using Checkstyle **10.12.7**
