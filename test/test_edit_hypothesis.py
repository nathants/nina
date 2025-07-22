
import re
import time
import sys
import os
import subprocess
import tempfile
import random
import glob
from pathlib import Path
from hypothesis import given, strategies as st, settings, assume, example
from hypothesis.database import ExampleDatabase
import json

# Select random code files excluding node_modules, large or binary.
# Scans current project and limited repos for files under 100KB.
# Returns list of valid file paths for hypothesis tests.
def get_random_code_file():
    # Start with current project
    current_files = []
    extensions = [".py", ".go", ".js", ".ts", ".tsx", ".cpp", ".c", ".java", ".rs", ".h", ".hpp"]

    # Scan current directory first
    for ext in extensions:
        pattern = f"**/*{ext}"
        current_files.extend(glob.glob(pattern, recursive=True))

    # Limit to a few specific repos to avoid scanning 260k+ files
    repo_path = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))

    all_files = current_files[:]

    # Add files from specific repos with limits
    if os.path.exists(repo_path):
        repo_files = []
        for ext in extensions:
            pattern = os.path.join(repo_path, "**", f"*{ext}")
            repo_files.extend(glob.glob(pattern, recursive=True)[:50])  # Limit per repo
        all_files.extend(repo_files)

    # Filter out very large files and binary files
    valid_files = []
    for f in all_files[:500]:  # Hard limit to prevent hangs
        try:
            # Skip node_modules and other unwanted directories
            if 'node_modules' in f or '__pycache__' in f or '.git' in f or 'test' in f:
                continue
            size = os.path.getsize(f)
            if size > 0 and size < 100_000:  # Less than 100KB
                valid_files.append(f)
        except:
            pass

    return valid_files


def extract_line_range(content, start_line, num_lines):
    """Extract a range of lines from content"""
    lines = content.splitlines(keepends=True)
    if start_line >= len(lines):
        return None

    end_line = min(start_line + num_lines, len(lines))
    return lines[start_line:end_line]


def damage_search_text(text, damage_type):
    if damage_type == "add_whitespace":
        # Add a single space at a random position
        if not text:
            return text
        pos = random.randint(0, len(text))
        return text[:pos] + ' ' + text[pos:]

    elif damage_type == "remove_whitespace":
        # Remove a single space at a random position
        space_positions = [i for i, c in enumerate(text) if c == ' ']
        if not space_positions:
            return text
        pos = random.choice(space_positions)
        return text[:pos] + text[pos+1:]

    elif damage_type == "minor_typo":
        # Introduce minor typos in variable names
        chars = list(text)
        if len(chars) > 10:
            # Pick a random alphanumeric char and change it
            indices = [i for i, c in enumerate(chars) if c.isalnum()]
            if indices:
                idx = random.choice(indices)
                if chars[idx].islower():
                    chars[idx] = chars[idx].upper()
                elif chars[idx].isupper():
                    chars[idx] = chars[idx].lower()
        return ''.join(chars)

    return text


def build_edit_binary():
    """Build the edit binary to /tmp/nina"""
    subprocess.check_call(["go", "build", "-o", "/tmp/nina"])


def run_edit_cmd(search_text, replace_text, target_content):
    """Run the edit command and return result"""
    # Build edit binary before running
    build_edit_binary()
    tmpdir = tempfile.mkdtemp()
    search_file = os.path.join(tmpdir, "search.txt")
    replace_file = os.path.join(tmpdir, "replace.txt")
    contents_file = os.path.join(tmpdir, "contents.txt")

    with open(search_file, 'w') as f:
        f.write(search_text)
    with open(replace_file, 'w') as f:
        f.write(replace_text)
    with open(contents_file, 'w') as f:
        f.write(target_content)

    # Get list of debug files before running edit
    debug_dir = os.path.expanduser("~/.nina-debug")
    before_files = set()
    if os.path.exists(debug_dir):
        before_files = set(os.listdir(debug_dir))

    try:
        subprocess.check_call(["/tmp/nina", "edit", search_file, replace_file, contents_file])
    except subprocess.CalledProcessError as e:
        # Find new debug files created
        after_files = set(os.listdir(debug_dir)) if os.path.exists(debug_dir) else set()
        new_files = sorted(after_files - before_files)

        print("\n" + "="*80)
        print("EDIT COMMAND FAILED - Debug files created:")
        print("="*80)
        for f in new_files:
            path = os.path.join(debug_dir, f)
            print(f"  {path}")
        print("="*80)
        print(f"Temporary directory: {tmpdir}")
        print("="*80 + "\n")

        raise

    with open(contents_file, 'r') as f:
        return f.read(), tmpdir

class FileRangeDamage:
    """Represents a test case for damaged search patterns"""
    def __init__(self, file_path, start_line, num_lines, damage_type):
        self.file_path = file_path
        self.start_line = start_line
        self.num_lines = num_lines
        self.damage_type = damage_type


# Lazy load available files to avoid hanging on import
AVAILABLE_FILES = None

def get_available_files():
    """Lazy load available files"""
    global AVAILABLE_FILES
    if AVAILABLE_FILES is None:
        AVAILABLE_FILES = get_random_code_file()
    return AVAILABLE_FILES


@st.composite
def file_range_damage(draw):
    """Strategy to generate file range with damage type"""
    available_files = get_available_files()
    if not available_files:
        assume(False)  # Skip if no files available

    file_path = draw(st.sampled_from(available_files))

    # Read file to get line count
    try:
        with open(file_path, 'r') as f:
            content = f.read()
            lines = content.splitlines()[:1000]
            line_count = len(lines)
    except:
        assume(False)  # Skip unreadable files

    assume(line_count > 0)  # Skip empty files

    # Try to find a non-empty line range
    start_line = draw(st.integers(min_value=0, max_value=max(0, line_count - 1)))
    max_lines = min(50, line_count - start_line)
    num_lines = draw(st.integers(min_value=1, max_value=max(1, max_lines)))

    # Check if the selected range has non-empty content
    selected_lines = lines[start_line:start_line + num_lines]
    selected_text = '\n'.join(selected_lines)
    assume(selected_text.strip())  # Skip if selected range is empty

    # Check that the selected sequence appears exactly once in the file
    full_text = '\n'.join(lines)
    occurrences = full_text.count(selected_text)
    assume(occurrences == 1)  # Skip if sequence appears multiple times

    damage_type = draw(st.sampled_from([
        "add_whitespace",
        "remove_whitespace",
        "minor_typo"
    ]))


    return FileRangeDamage(file_path, max(0, start_line - 3), num_lines + 6, damage_type) # add 3 lines of padding

@given(test_case=file_range_damage())
@settings(max_examples=10, deadline=1 * 60 * 1000)
def test_edit_with_damaged_search(test_case):
    """Test that edit cmd finds correct lines even with damaged search patterns"""

    # Read the target file
    with open(test_case.file_path, 'r') as f:
        content = f.read()

    # Extract the line range
    lines = content.splitlines(keepends=True)
    search_lines = lines[test_case.start_line:test_case.start_line + test_case.num_lines]
    search_text = ''.join(search_lines)

    if not search_text.strip():
        print("SKIPPED: Empty search text")
        return  # Skip empty search


    damaged_search = search_text
    damage_iterations = random.randint(1, 10)
    for _ in range(damage_iterations):
        damaged_search = damage_search_text(damaged_search, test_case.damage_type)

    # Create a unique replacement to verify the edit worked
    replace_marker = f"HYPOTHESIS_TEST_REPLACEMENT_{random.randint(1000, 9999)}"
    replace_text = f"# {replace_marker}\n"

    # Run edit command and verify correct lines were selected
    then = time.time()
    print("\nrun edit", test_case.file_path, len(damaged_search.splitlines()))

    try:
        result_content, tmpdir = run_edit_cmd(damaged_search, replace_text, content)
    except:
        # Exit immediately on failure - hypothesis will have received the exception
        import traceback
        traceback.print_exc()
        print("\nTest failed, exiting without shrinking.", flush=True)
        time.sleep(.1)
        os._exit(1)

    print("tempdir", tmpdir, int(time.time() - then))

    # Verify the edit worked by checking if replacement happened at correct location
    result_lines = result_content.splitlines(keepends=True)

    # Check that the replacement marker appears in the result
    if replace_marker not in result_content:
        # Find new debug files created
        after_files = set(os.listdir(debug_dir)) if os.path.exists(debug_dir) else set()
        new_files = sorted(after_files - before_files)

        print("\n" + "="*80)
        print("ASSERTION FAILED - Debug files created:")
        print("="*80)
        for f in new_files:
            path = os.path.join(debug_dir, f)
            print(f"  {path}")
        print("="*80)
        print(f"Replacement marker not found in result")
        print(f"Temporary directory: {tmpdir}")
        print("="*80 + "\n")
        sys.exit(1)

    # Find where the replacement occurred
    replacement_start = None
    for i, line in enumerate(result_lines):
        if replace_marker in line:
            replacement_start = i
            break

    if replacement_start is None:
        # Find new debug files created
        after_files = set(os.listdir(debug_dir)) if os.path.exists(debug_dir) else set()
        new_files = sorted(after_files - before_files)

        print("\n" + "="*80)
        print("ASSERTION FAILED - Debug files created:")
        print("="*80)
        for f in new_files:
            path = os.path.join(debug_dir, f)
            print(f"  {path}")
        print("="*80)
        print(f"Could not find replacement start line")
        print(f"Temporary directory: {tmpdir}")
        print("="*80 + "\n")
        sys.exit(1)

    # The replacement should be at the same line number as our original search
    # Allow some tolerance for whitespace/newline differences
    line_diff = abs(replacement_start - test_case.start_line)
    if line_diff > 2:
        # Find new debug files created
        after_files = set(os.listdir(debug_dir)) if os.path.exists(debug_dir) else set()
        new_files = sorted(after_files - before_files)

        print("\n" + "="*80)
        print("ASSERTION FAILED - Debug files created:")
        print("="*80)
        for f in new_files:
            path = os.path.join(debug_dir, f)
            print(f"  {path}")
        print("="*80)
        print(f"Edit occurred at wrong location: expected around line {test_case.start_line}, got {replacement_start}")
        print(f"Temporary directory: {tmpdir}")
        print("="*80 + "\n")
        sys.exit(1)
