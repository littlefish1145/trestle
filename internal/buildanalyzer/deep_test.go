package buildanalyzer

import "testing"

func TestCompilerTimingAdapters(t *testing.T) {
	for _, fixture := range []struct {
		adapter, raw, phase string
		durationUS          float64
	}{
		{"clang", `{"traceEvents":[{"name":"cc1","ph":"X","dur":500}]}`, "cc1", 500},
		{"gcc", "Time variable usr sys wall GGC\n phase parsing : 0.01 ( 10%) 0.02 (20%) 0.12 (34%) 100k", "phase parsing", 120000},
		{"msvc", "time(c2.dll)=0.013s < 1 - 2 > BB", "c2.dll", 13000},
		{"nvcc", "source file name , phase name , phase input files , phase output file , arch , tool, metric , unit\nx.cu , ptxas , in , out , sm_75 , nvcc , 0.75 , ms\n", "ptxas", 750},
	} {
		events, err := parseCompiler(fixture.adapter, []byte(fixture.raw))
		if err != nil || len(events) != 1 || events[0] != (CompilerEvent{fixture.phase, fixture.durationUS}) {
			t.Errorf("%s timing: %+v, %v", fixture.adapter, events, err)
		}
	}
	if _, err := parseCompiler("clang", []byte("incomplete")); err == nil {
		t.Fatal("malformed compiler trace accepted")
	}
}
