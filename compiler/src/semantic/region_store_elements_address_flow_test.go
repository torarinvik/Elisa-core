package semantic

import (
	"strings"
	"testing"
)

const storeElementsAddressFlowSource = `global region source
struct Payload:
    text: sview @source
    address: uintptr

struct Source:
    values: darray[Payload]

struct Table:
    views: mutable darray[sview] = []
    addresses: mutable darray[uintptr] = []
    entries: mutable darray[Payload] = []

def inspect_address(table: mutable Table&, address: uintptr) -> void:
    if address == 0:
        return

def record_address(table: mutable Table&, address: uintptr) -> void:
    can Memory.Allocate, Abort.Panic:
        table.addresses.push(address)

def forward_address(table: mutable Table&, address: uintptr) -> void:
    record_address(table, address)

def identity_address(address: uintptr) -> uintptr:
    return address

def copy_address(out: mutable uintptr&, address: uintptr) -> void:
    out <- address

def keep_safe_view_with_readonly_address(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.views.push(state.values[0].text)
        inspect_address(table, state.values[0].address)

def keep_one_hop_mixed(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        value: Payload = state.values[0]
        table.views.push(value.text)
        record_address(table, value.address)

def keep_two_hop_mixed(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        value: Payload = state.values[0]
        table.views.push(value.text)
        forward_address(table, value.address)

def keep_assigned_return_address(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        address: uintptr = identity_address(state.values[0].address)
        forward_address(table, address)

def keep_direct_return_address(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        forward_address(table, identity_address(state.values[0].address))

def keep_call_written_address(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        address: mutable uintptr = 0
        copy_address(&address, state.values[0].address)
        forward_address(table, address)

def keep_whole_payload(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.entries.push(state.values[0])

def get_payload(state: Source&) -> Payload:
    return state.values[0]

def keep_returned_payload(table: mutable Table&, state: Source&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.entries.push(get_payload(state))

`

func TestStoreElementsReadOnlyAddressHelperDoesNotPoisonProof(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_readonly_address.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	decl := storeScanFindFunc(t, result, "keep_safe_view_with_readonly_address")
	a := result.analyzer
	previousScope := a.currentScope
	a.currentScope = a.globalScope
	defer func() { a.currentScope = previousScope }()
	if a.storeScanParamMayBeWritten(storeScanFindFunc(t, result, "inspect_address"), 0) {
		t.Fatal("the read-only address helper must not be classified as writing its address-capable Table parameter")
	}
	got := a.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{})
	if !got {
		t.Fatal("a read-only uintptr helper must not poison an otherwise program-lived view store")
	}
}

func TestStoreElementsOneHopAddressForwardingFailsClosedWithResolvedCallee(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_one_hop_resolved_address.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	decl := storeScanFindFunc(t, result, "keep_one_hop_mixed")
	previousScope := result.analyzer.currentScope
	result.analyzer.currentScope = result.analyzer.globalScope
	defer func() { result.analyzer.currentScope = previousScope }()
	if result.analyzer.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{}) {
		t.Fatal("resolved one-hop forwarding of a source-derived uintptr to storage must fail closed")
	}
}

func TestStoreElementsTwoHopAddressForwardingFailsClosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_two_hop_address.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	decl := storeScanFindFunc(t, result, "keep_two_hop_mixed")
	previousScope := result.analyzer.currentScope
	result.analyzer.currentScope = result.analyzer.globalScope
	defer func() { result.analyzer.currentScope = previousScope }()
	if result.analyzer.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{}) {
		t.Fatal("two-hop forwarding of a source-derived uintptr to storage must fail closed")
	}
}

func TestStoreElementsWholePayloadAddressFailsClosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_whole_payload_address.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	decl := storeScanFindFunc(t, result, "keep_whole_payload")
	a := result.analyzer
	previousScope := a.currentScope
	a.currentScope = a.globalScope
	defer func() { a.currentScope = previousScope }()
	if got := a.calleeStoreModeInto(decl, 1, 0); got == storeNone {
		t.Fatal("whole Payload storage containing a source view and uintptr must be visible to the store scan")
	}
	if a.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{}) {
		t.Fatal("a whole Payload sink must fail closed when its aggregate contains uintptr")
	}
}

func TestStoreElementsReturnedWholePayloadAddressFailsClosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_returned_payload_address.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	a := result.analyzer
	previousScope := a.currentScope
	a.currentScope = a.globalScope
	defer func() { a.currentScope = previousScope }()
	getPayload := storeScanFindFunc(t, result, "get_payload")
	if got := a.calleeReturnModeOf(getPayload, 0, map[storeScanKey]bool{}); got == storeNone {
		t.Fatal("returning a whole Payload with uintptr must retain source flow in the return summary")
	}
	keep := storeScanFindFunc(t, result, "keep_returned_payload")
	if a.calleeStoresProgramLivedElements(keep, 1, 0, map[storeScanKey]bool{}) {
		t.Fatal("a returned whole Payload sink must fail closed when its aggregate contains uintptr")
	}
}

func TestStoreElementsAssignedAddressReturnFailsClosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_assigned_address_return.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	a := result.analyzer
	previousScope := a.currentScope
	a.currentScope = a.globalScope
	defer func() { a.currentScope = previousScope }()
	decl := storeScanFindFunc(t, result, "keep_assigned_return_address")
	if got := a.calleeStoreModeInto(decl, 1, 0); got == storeNone {
		t.Fatal("a source-derived uintptr returned into a local and forwarded to storage must retain source flow")
	}
	if a.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{}) {
		t.Fatal("an assigned source-derived uintptr return forwarded to storage must fail closed")
	}
}

func TestStoreElementsAddressWrittenThroughCallFailsClosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_address_call_write.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	a := result.analyzer
	previousScope := a.currentScope
	a.currentScope = a.globalScope
	defer func() { a.currentScope = previousScope }()
	decl := storeScanFindFunc(t, result, "keep_call_written_address")
	if !a.storeScanParamMayBeWritten(storeScanFindFunc(t, result, "copy_address"), 0) {
		t.Fatal("the store scanner must recognize writes through a mutable uintptr reference parameter")
	}
	if got := a.calleeStoreModeInto(decl, 1, 0); got == storeNone {
		t.Fatal("a source-derived uintptr written through an output call and forwarded to storage must retain source flow")
	}
	if a.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{}) {
		t.Fatal("an address copied through a call output and forwarded to storage must fail closed")
	}
}

func TestStoreElementsDirectAddressReturnFailsClosed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_elements_direct_address_return.elisa", storeElementsAddressFlowSource, AnalyzeOptions{})
	a := result.analyzer
	previousScope := a.currentScope
	a.currentScope = a.globalScope
	defer func() { a.currentScope = previousScope }()
	decl := storeScanFindFunc(t, result, "keep_direct_return_address")
	if got := a.calleeStoreModeInto(decl, 1, 0); got == storeNone {
		t.Fatal("a source-derived uintptr returned directly into a storage call must retain source flow")
	}
	if a.calleeStoresProgramLivedElements(decl, 1, 0, map[storeScanKey]bool{}) {
		t.Fatal("a direct returned uintptr forwarded to storage must fail closed")
	}
}

func TestStoreElementsWholePayloadLocalSourceRejectedAtCallSite(t *testing.T) {
	source := storeElementsAddressFlowSource + `
def caller(table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        region short(4096):
            values: mutable darray[Payload] @short = [Payload{text: "static", address: 0}]
            state: Source = Source{values: values}
            keep_whole_payload(table, state)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_elements_whole_payload_local_source.elisa", source)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "call may store a value in region") {
		t.Fatalf("expected the local source aggregate to be rejected at the call site, got:\n%s", all)
	}
}
