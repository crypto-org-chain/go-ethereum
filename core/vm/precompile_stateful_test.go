// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package vm

import (
	"maps"
	"slices"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// recordingPrecompile is a stateful precompile stand-in recording the readonly
// flag of every invocation.
type recordingPrecompile struct {
	addr     common.Address
	readOnly []bool
}

func (p *recordingPrecompile) Address() common.Address   { return p.addr }
func (p *recordingPrecompile) RequiredGas([]byte) uint64 { return 0 }
func (p *recordingPrecompile) Name() string              { return "RECORDING" }
func (p *recordingPrecompile) Run(evm *EVM, contract *Contract, readonly bool) ([]byte, error) {
	p.readOnly = append(p.readOnly, readonly)
	return nil, nil
}

// callerCode returns code invoking target with the given call opcode, forwarding
// all gas and no value, then stopping.
func callerCode(op OpCode, target byte) []byte {
	code := []byte{byte(PUSH1), 0, byte(PUSH1), 0, byte(PUSH1), 0, byte(PUSH1), 0}
	if op == CALL {
		code = append(code, byte(PUSH1), 0) // value
	}
	return append(code, byte(PUSH1), target, byte(GAS), byte(op), byte(STOP))
}

// TestStatefulPrecompileReadOnly checks that a stateful precompile reached by a
// plain CALL inherits the static context of the frame making the call.
func TestStatefulPrecompileReadOnly(t *testing.T) {
	var (
		outer = common.BytesToAddress([]byte{0xaa})
		inner = common.BytesToAddress([]byte{0xbb})
	)
	for _, tc := range []struct {
		name  string
		outer OpCode
		want  bool
	}{
		{"call", CALL, false},
		{"staticcall", STATICCALL, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &recordingPrecompile{addr: common.BytesToAddress([]byte{0x65})}

			statedb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
			statedb.SetCode(outer, callerCode(tc.outer, 0xbb), tracing.CodeChangeUnspecified)
			statedb.SetCode(inner, callerCode(CALL, 0x65), tracing.CodeChangeUnspecified)

			blockCtx := BlockContext{
				CanTransfer: func(StateDB, common.Address, *uint256.Int) bool { return true },
				Transfer:    func(StateDB, common.Address, common.Address, *uint256.Int, *params.Rules) {},
				BlockNumber: common.Big0,
			}
			evm := NewEVM(blockCtx, statedb, params.TestChainConfig, Config{})
			precompiles := DefaultPrecompiles(evm.chainRules)
			precompiles[probe.addr] = probe
			evm.SetPrecompiles(precompiles)

			if _, _, err := evm.Call(common.Address{}, outer, nil, NewGasBudget(1_000_000, 0), new(uint256.Int)); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if len(probe.readOnly) != 1 || probe.readOnly[0] != tc.want {
				t.Fatalf("precompile saw readonly %v, want [%v]", probe.readOnly, tc.want)
			}
		})
	}
}

// TestDefaultPrecompilesIsCopy checks that modifying the returned set leaves
// the shared default set untouched.
func TestDefaultPrecompilesIsCopy(t *testing.T) {
	addr := common.BytesToAddress([]byte{0x65})
	DefaultPrecompiles(params.Rules{IsByzantium: true})[addr] = &recordingPrecompile{}
	if _, ok := PrecompiledContractsByzantium[addr]; ok {
		t.Fatal("DefaultPrecompiles returned the shared default set")
	}
}

func TestValidatePrecompiles(t *testing.T) {
	var (
		addr  = common.BytesToAddress([]byte{0x65})
		valid = &recordingPrecompile{addr: addr}
	)
	for _, tc := range []struct {
		name        string
		precompiles PrecompiledContracts
		active      []common.Address
		wantErr     bool
	}{
		{"valid", PrecompiledContracts{addr: valid}, []common.Address{addr}, false},
		{"length mismatch", PrecompiledContracts{addr: valid}, nil, true},
		{"missing", PrecompiledContracts{common.BytesToAddress([]byte{0x66}): valid}, []common.Address{addr}, true},
		{"nil", PrecompiledContracts{addr: nil}, []common.Address{addr}, true},
		{"zero address", PrecompiledContracts{{}: &recordingPrecompile{}}, []common.Address{{}}, true},
		{"address mismatch", PrecompiledContracts{addr: &recordingPrecompile{addr: common.BytesToAddress([]byte{0x66})}}, []common.Address{addr}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePrecompiles(tc.precompiles, tc.active); (err != nil) != tc.wantErr {
				t.Fatalf("got error %v, want error %v", err, tc.wantErr)
			}
		})
	}
}

// TestDefaultPrecompilesValid checks every built-in precompile set passes
// ValidatePrecompiles, so each precompile reports the address it is
// registered under.
func TestDefaultPrecompilesValid(t *testing.T) {
	for name, set := range map[string]PrecompiledContracts{
		"homestead": PrecompiledContractsHomestead,
		"byzantium": PrecompiledContractsByzantium,
		"istanbul":  PrecompiledContractsIstanbul,
		"berlin":    PrecompiledContractsBerlin,
		"cancun":    PrecompiledContractsCancun,
		"prague":    PrecompiledContractsPrague,
		"osaka":     PrecompiledContractsOsaka,
		"p256":      PrecompiledContractsP256Verify,
	} {
		if err := ValidatePrecompiles(set, slices.Collect(maps.Keys(set))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
