import time
import sys
import subprocess

print("Starting timeout plugin...")
print("Spawning a child process that sleeps forever...")
# Spawn a child process that won't die easily
subprocess.Popen(["sleep", "3600"])
print("Sleeping myself...")
time.sleep(3600)
