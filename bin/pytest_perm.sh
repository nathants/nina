#!/bin/bash
set -eou pipefail

pytest test/test_edit_examples.py -vvx -s --tb=native
