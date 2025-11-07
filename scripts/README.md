# Git Hooks and License Management

This directory contains git hooks and scripts to ensure code quality and license compliance.

## Setup

### 1. Install addlicense

First, install the `addlicense` tool:

```bash
go install github.com/google/addlicense@latest
```

### 2. Install Git Hooks

Run the installation script to set up the pre-commit hook:

```bash
./scripts/install-hooks.sh
```

This will install a pre-commit hook that automatically checks for GPL-3.0-or-later license headers before allowing commits.

## Usage

### Checking License Headers

To check all files for license headers:

```bash
addlicense -check -c "Spruce Health" -s -l gpl-3.0-or-later \
  -ignore ".git/**" -ignore "vendor/**" -ignore ".idea/**" -ignore "**/*.md" .
```

### Adding License Headers

To automatically add missing license headers to all files:

```bash
addlicense -c "Spruce Health" -s -l gpl-3.0-or-later \
  -ignore ".git/**" -ignore "vendor/**" -ignore ".idea/**" -ignore "**/*.md" .
```

### Adding License to Specific Files

To add license headers to specific files:

```bash
addlicense -c "Spruce Health" -s -l gpl-3.0-or-later file1.go file2.html
```

## Pre-commit Hook

The pre-commit hook automatically runs before each commit and:

1. Checks all staged Go, HTML, and CSS files for GPL-3.0-or-later license headers
2. Blocks the commit if any files are missing license headers
3. Provides instructions on how to add the missing headers

If the hook blocks your commit, simply run the `addlicense` command to add headers, then stage and commit again.

## License Header Format

### Go Files
```go
// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health
```

### HTML Files
```html
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
<!-- Copyright (c) 2025 Spruce Health -->
```

### CSS Files
```css
/* SPDX-License-Identifier: GPL-3.0-or-later */
/* Copyright (c) 2025 Spruce Health */
```

## Files in This Directory

- `pre-commit` - Git pre-commit hook that checks for license headers
- `install-hooks.sh` - Script to install git hooks
- `README.md` - This file
