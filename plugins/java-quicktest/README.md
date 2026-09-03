# java-quicktest ActionD Plugin

Smart test selection for Java/Maven projects based on AST diff analysis.

## Features
- Analyzes code changes using JavaParser
- Selects only affected tests (direct + one-hop dependencies)
- Falls back to full tests for high-risk changes
- Outputs detailed audit report

## Trigger
- `git.push`
- `git.commit`

## Requirements
- Java 17+
- Maven
- TestNG test framework

## Output Artifacts
- `quicktest-analysis.json` - Analysis result with selected tests
- `test-output.log` - Test execution output

## How it works
1. Detects Java file changes via `git diff HEAD~1`
2. Runs `java-quicktest` to analyze AST changes
3. Identifies affected tests via naming convention + call graph
4. Executes only selected tests with Maven
5. Reports pass/fail status
