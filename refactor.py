import re

import sys

with open("Code/llcontext_atpl/src/atpl_frontend.llcontext", "r") as f:
    text = f.read()

def repl(pattern, replacement):
    global text
    text = re.sub(pattern, replacement, text)

# I will just write a few replacements.
# test passing one by one.

