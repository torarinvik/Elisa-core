# Elisa-aware lldb formatters.
#
# Load with:   command script import <path>/elisa_lldb.py
# or add that line to ~/.lldbinit so it loads automatically.
#
# Requires the program to be built with -g (DWARF debug info). Provides:
#   - darray[T]: a summary ("count=N [e0, e1, ...]") and expandable element children.
#   - Arena:     a summary of its cursor/size when those fields are present.
#
# These rely only on the DWARF the Elisa backend emits (darray exposes items/count/
# capacity members), so they are general -- any Elisa program, not just the emulator.

import lldb

_DARRAY_PREVIEW = 16  # max elements shown inline / materialized


def _darray_fields(valobj):
    # Read the raw struct members; a synthetic children provider (registered below)
    # otherwise shadows them with the element views, hiding count/items.
    raw = valobj.GetNonSyntheticValue()
    count = raw.GetChildMemberWithName("count").GetValueAsUnsigned(0)
    items = raw.GetChildMemberWithName("items")
    return count, items


def darray_summary(valobj, internal_dict):
    try:
        count, items = _darray_fields(valobj)
        base = items.GetValueAsUnsigned(0)
        if count == 0 or base == 0:
            return "count=%d []" % count
        elem_type = items.GetType().GetPointeeType()
        elem_size = elem_type.GetByteSize()
        parts = []
        limit = min(count, _DARRAY_PREVIEW)
        for i in range(limit):
            ev = valobj.CreateValueFromAddress("[%d]" % i, base + i * elem_size, elem_type)
            parts.append(ev.GetValue() or ev.GetSummary() or "?")
        more = "" if count <= limit else ", ... +%d" % (count - limit)
        return "count=%d [%s%s]" % (count, ", ".join(parts), more)
    except Exception as e:  # never let a formatter break the session
        return "<darray: %s>" % e


class DArrayChildrenProvider(object):
    def __init__(self, valobj, internal_dict):
        self.valobj = valobj
        self.count = 0
        self.base = 0
        self.elem_type = None
        self.elem_size = 1

    def update(self):
        try:
            self.count, items = _darray_fields(self.valobj)
            self.base = items.GetValueAsUnsigned(0)
            self.elem_type = items.GetType().GetPointeeType()
            self.elem_size = max(1, self.elem_type.GetByteSize())
        except Exception:
            self.count = 0
        return False

    def num_children(self):
        if self.base == 0:
            return 0
        return min(self.count, _DARRAY_PREVIEW)

    def get_child_index(self, name):
        try:
            return int(name.lstrip("[").rstrip("]"))
        except Exception:
            return -1

    def get_child_at_index(self, index):
        if index < 0 or index >= self.num_children() or self.base == 0:
            return None
        return self.valobj.CreateValueFromAddress("[%d]" % index, self.base + index * self.elem_size, self.elem_type)


def arena_summary(valobj, internal_dict):
    try:
        bits = []
        for field in ("offset", "cursor", "size", "capacity", "used"):
            child = valobj.GetChildMemberWithName(field)
            if child and child.IsValid():
                bits.append("%s=%d" % (field, child.GetValueAsUnsigned(0)))
        return " ".join(bits) if bits else "<arena>"
    except Exception as e:
        return "<arena: %s>" % e


def __lldb_init_module(debugger, internal_dict):
    mod = __name__
    debugger.HandleCommand('type summary add -x "^darray\\[" -F %s.darray_summary' % mod)
    debugger.HandleCommand('type synthetic add -x "^darray\\[" -l %s.DArrayChildrenProvider' % mod)
    debugger.HandleCommand('type summary add Arena -F %s.arena_summary' % mod)
    print("elisa lldb formatters loaded (darray, Arena)")
