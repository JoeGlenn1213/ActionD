import time
import sys

print("Starting faulty plugin...")
print("This will simulate a build failure that the interpreter should catch.")
print("BUILD FAILURE: Could not resolve dependencies", file=sys.stderr)
sys.exit(1)
