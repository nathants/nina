
# Example-based tests for edit cmd using shrunk/failed examples from hypothesis testing.
# These are regression tests to ensure previously failed cases continue to work.
# Examples are loaded from failed_examples.json and folder-based permanent_examples.

from pathlib import Path
import os
import json
import subprocess
import tempfile
import pytest


def build_edit_binary():
    """Build the edit binary to /tmp/nina-edit"""
    build_result = subprocess.run(
        ["go", "build", "-o", "/tmp/nina-edit", "../cmd/edit/main.go"],
        capture_output=True,
        text=True
    )
    if build_result.returncode != 0:
        raise RuntimeError(f"Failed to build edit binary: {build_result.stderr}")


def run_edit_cmd(search_text, replace_text, target_content):
    """Run the edit command and return result"""
    # Build edit binary before running
    build_edit_binary()
    
    with tempfile.TemporaryDirectory() as tmpdir:
        search_file = os.path.join(tmpdir, "search.txt")
        replace_file = os.path.join(tmpdir, "replace.txt")
        target_file = os.path.join(tmpdir, "target.txt")
        
        with open(search_file, 'w') as f:
            f.write(search_text)
        with open(replace_file, 'w') as f:
            f.write(replace_text)
        with open(target_file, 'w') as f:
            f.write(target_content)
        
        # Run edit command
        result = subprocess.run(
            ["/tmp/nina-edit", search_file, replace_file, target_file],
            capture_output=True,
            text=True
        )
        
        if result.returncode == 0:
            with open(target_file, 'r') as f:
                return f.read(), None
        else:
            return None, result.stderr


def load_examples():
    """Load test examples from folder-based files"""
    examples = []
    
    # Load permanent examples from directories
    base = Path("permanent_examples")
    if base.exists():
        for example_dir in sorted(p for p in base.iterdir() if p.is_dir()):
            name = example_dir.name
            target_content = (example_dir / "target.txt").read_text()
            search_text = (example_dir / "search.txt").read_text()
            replace_text = (example_dir / "replace.txt").read_text()
            expected_result = ((example_dir / "expected.txt").read_text()
                               if (example_dir / "expected.txt").exists()
                               else None)
            meta = {}
            meta_path = example_dir / "meta.json"
            if meta_path.exists():
                meta = json.loads(meta_path.read_text())
            example = {
                "name": name,
                "target_content": target_content,
                "search_text": search_text,
                "replace_text": replace_text,
                "expected_result": expected_result,
                "should_fail": meta.get("should_fail", False),
                "source": "permanent",
            }
            examples.append(example)
    
    # Load failed examples (from hypothesis testing)
    failed_file = "test/failed_examples.json"
    if os.path.exists(failed_file):
        with open(failed_file, 'r') as f:
            failed = json.load(f)
            for ex in failed:
                ex['source'] = 'failed'
                examples.append(ex)
    
    return examples


class TestEditExamples:
    """Test class for example-based regression tests"""
    
    @pytest.mark.parametrize("example", load_examples())
    def test_example(self, example):
        """Test a specific example case"""
        
        # For failed examples, we test that they now work
        if example['source'] == 'failed':
            # These previously failed, so we test with undamaged search
            with open(example['file_path'], 'r') as f:
                content = f.read()
            
            lines = content.splitlines(keepends=True)
            search_lines = lines[example['start_line']:example['start_line'] + example['num_lines']]
            search_text = ''.join(search_lines)
            
            replace_text = "# EXAMPLE_TEST_REPLACEMENT\n"
            result_content, error = run_edit_cmd(search_text, replace_text, content)
            
            assert error is None, f"Edit failed: {error}"
            assert replace_text in result_content
        
        # For permanent examples, test as specified
        else:
            target_content = example['target_content']
            search_text = example['search_text']
            replace_text = example['replace_text']
            expected_result = example.get('expected_result')
            should_fail = example.get('should_fail', False)
            
            result_content, error = run_edit_cmd(search_text, replace_text, target_content)
            
            if should_fail:
                assert error is not None, "Expected failure but edit succeeded"
            else:
                assert error is None, f"Edit failed: {error}"
                if expected_result:
                    assert result_content == expected_result
                else:
                    assert replace_text in result_content




if __name__ == "__main__":
    # Run tests
    pytest.main([__file__, "-v"])
