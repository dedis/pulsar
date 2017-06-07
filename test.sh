#!/usr/bin/env bash

DBG_TEST=2
# Debug-level for app
DBG_APP=3
DBG_SRV=2
NBR_SERVERS=10
NBR_SERVERS_GROUP=10

. $GOPATH/src/gopkg.in/dedis/onet.v1/app/libtest.sh

main(){
	startTest
	buildConode "github.com/dedis/pulsar/randhound/service"
#	test App
	test Index
	stopTest
}

testIndex(){
	rm co*/
	runCoBG $(seq $NBR_SERVERS)
	testOK runTmpl setup -i 100 public.toml
	testOK runTmpl random public.toml
	sleep 1
	testOK runTmpl random public.toml
	testOK runTmpl random -i 1 public.toml
	testOK runTmpl random -i 1 public.toml
}

testApp(){
	runCoBG $(seq $NBR_SERVERS)
	testFail runTmpl random public.toml
	testOK runTmpl setup -i 100 public.toml
	testOK runTmpl random public.toml
	sleep 2
	testOK runTmpl random public.toml
}

testBuild(){
	testOK dbgRun runTmpl --help
}

runTmpl(){
	dbgRun ./$APP -d $DBG_APP $@
}

main
