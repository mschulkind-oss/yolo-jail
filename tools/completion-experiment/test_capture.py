#!/bin/python3
"""Test-first owned-file contract. Injected metadata is not a real tool/quality result."""
import sys
sys.dont_write_bytecode=True
from pathlib import Path
import hashlib,json,os,tempfile,unittest
OUT=Path(__file__).resolve().parent

def run_fixture(case):
    CASE=Path(case)
    assert CASE.name and not CASE.exists()
    CASE.mkdir()
    try:
     import capture as c
    except ImportError as exc:
     (CASE/'results.json').write_text(json.dumps({'state':'red-interface-absent','error':str(exc),'qualitySuccess':False},indent=2)+'\n')
     raise

    root=CASE/'fixture';root.mkdir();go=root/'goroot';(go/'bin').mkdir(parents=True)
    (go/'bin/go').write_bytes(b'owned non-tool metadata binding\n');(go/'VERSION').write_text('go1.26.7\n')
    (go/'go.env').write_text('CGO_CFLAGS=-O2 -g\n');(go/'src').mkdir();(go/'pkg/tool').mkdir(parents=True)
    (go/'bin/go').chmod(0o755)
    repo=root/'repo';repo.mkdir();(repo/'go.mod').write_text('module fixture\ngo 1.26.0\n');(repo/'go.sum').write_text('owned fixture sums\n')
    home=root/'home';(home/'.config/go').mkdir(parents=True);settings=home/'.config/go/env';settings.write_text('CGO_CPPFLAGS=-I'+str(root/'headers')+'\n')
    headers=root/'headers';headers.mkdir();(headers/'input.h').write_text('header input\n')
    env={'PATH':str(go/'bin'),'HOME':str(home),'GOROOT':str(go),'GOWORK':'off','GOENV':str(settings),'GOCACHE':str(root/'chosen-cache'),'GOMODCACHE':str(root/'chosen-module-cache')}
    metadata={k:'' for k in c.FIELDS}
    metadata.update(GOENV=str(settings),GOROOT=str(go),GOTOOLDIR=str(go/'pkg/tool'),GOVERSION='go1.26.7',GOHOSTOS='linux',GOHOSTARCH='amd64',GOOS='linux',GOARCH='amd64',GOMOD=str(repo/'go.mod'),GOWORK='off',GOCACHE=env['GOCACHE'],GOMODCACHE=env['GOMODCACHE'],CGO_ENABLED='1',CC='gcc',CXX='g++')
    calls=[]
    def runner(argv, *, cwd, env):
     assert argv[0]==str(go/'bin/go') and argv[1:3]==['env','-json']
     assert argv[3:]==list(c.FIELDS)
     assert env['HOME']==str(home) and env['GOENV']==str(settings) and env['GOWORK']=='off'
     assert env['GOCACHE']==str(root/'chosen-cache')
     calls.append({'argv':argv,'target':env.get('GOOS')})
     return dict(metadata,GOOS=env.get('GOOS','linux'))
    results=[]
    def ok(name,**fields):
     results.append(dict(control=name,passed=True,**fields));(CASE/'results.json').write_text(json.dumps(results,indent=2)+'\n')

    session=c.FullCapture(env,repo,runner=runner)
    try:session.capture_full(route='prose')
    except c.Refused:pass
    else:raise AssertionError('prose reached metadata command')
    assert not calls
    ok('non-full route refuses before every metadata argv')

    bad=c.FullCapture(env,repo,runner=lambda *a,**k:{'GOROOT':str(go)})
    try:bad.capture_full()
    except c.Unknown:pass
    else:raise AssertionError('missing effective metadata accepted')
    ok('missing whitelist fields are Unknown, never guessed')

    snapshot=session.capture_full()
    assert snapshot.metadata_state=='Known' and snapshot.profile_state=='Unknown'
    assert snapshot.official_state=='Unknown' and snapshot.quality_outcomes==[]
    assert snapshot.raw_metadata['base']['GOCACHE']==env['GOCACHE']
    assert snapshot.raw_metadata['base']['GOENV']==str(settings)
    assert all(x['target'] in (None,'linux','darwin') for x in calls)
    ok('full captures ordinary choices/recipe targets without quality or official-path guessing')

    before=len(calls);same=session.compare_prose(snapshot,env)
    assert same['sameContext'] and same['goFamilyArgv']==[] and len(calls)==before
    ok('unchanged prose comparison revalidates without any Go-family metadata argv')
    changed=dict(env,GOCACHE=str(root/'another-cache'))
    assert not session.compare_prose(snapshot,changed)['sameContext'] and len(calls)==before
    ok('explicit cache choice change invalidates without Go argv')

    old=settings.read_bytes();settings.write_text('CGO_CPPFLAGS=-I'+str(root/'other-headers')+'\n')
    assert not session.compare_prose(snapshot,env)['sameContext'] and len(calls)==before
    settings.write_bytes(old)
    ok('selected GOENV bytes invalidate former metadata without requery')

    # A scoped descriptor identifies a real descendant; restoring after the watch
    # barrier is a refusal, not equal-endpoint acceptance.
    fp=lambda:c.scoped_reads((),(headers,))
    initial=fp();old=(headers/'input.h').read_bytes()
    (headers/'input.h').write_text('changed dependency\n')
    assert initial.digest!=fp().digest
    (headers/'input.h').write_bytes(old)
    def mutate():
     (headers/'input.h').write_text('transient\n');(headers/'input.h').write_bytes(old)
    try:c.protected(fp,verify=mutate)
    except c.Mutated:pass
    else:raise AssertionError('restored dependency mutation accepted')
    ok('changed descendant and observed restored mutation defeat prior descriptor')

    provisional=fp();(headers/'new.h').write_text('new membership\n')
    final,obs=c.protected(fp,provisional=provisional)
    assert str(headers/'new.h') in final.members and final.digest!=provisional.digest
    ok('expanded input membership gets registration/reread through final validation',**obs)

    # Typed hooks validate the real command result, not a guessed default cache path.
    artifact=root/'actual-result-dir';(artifact/'bin').mkdir(parents=True)
    (artifact/'bin/go').write_text('owned fixture artifact, not executed\n');(artifact/'bin/go').chmod(0o755)
    (artifact/'VERSION').write_text('go1.26.7\n')
    result=c.OfficialResult('golang.org/toolchain@v0.0.1-go1.26.7.linux-amd64',str(artifact),'h1:'+('A'*43)+'=',0,True,'go version go1.26.7 linux/amd64',0)
    assert c.official_descriptor(result,'go1.26.7','linux','amd64')[0]=='Known'
    for invalid in (c.OfficialResult(result.module,result.directory,'',0,True,result.version_output,0),c.OfficialResult(result.module,result.directory,result.checksum,0,False,result.version_output,0),c.OfficialResult(result.module,result.directory,result.checksum,0,True,'wrong version',0)):
     assert c.official_descriptor(invalid,'go1.26.7','linux','amd64')[0]=='Unknown'
    ok('exact typed download Dir/Sum/version/readiness hook validates and refuses incomplete results')
    hook=c.OfficialHook()
    assert hook.result() is None
    hook.download('full',result.module,json.dumps({'Dir':str(artifact),'Sum':result.checksum}),0)
    assert hook.result() is None
    hook.readiness('full',str(artifact),True)
    hook.version('full',str(artifact/'bin/go'),result.version_output,0)
    assert hook.result()==result
    prepared=session.capture_full(official=hook.result())
    assert prepared.official_state=='Known' and prepared.profile_state=='Unknown' and prepared.quality_outcomes==[]
    ok('normal-full hook transitions missing result to observed chosen binding, not a manual setup embargo')

    # Flag membership source is identified, and its include tree is content-bound.
    flags=root/'cc-cflags';flags.write_text('-idirafter '+str(headers)+'\n')
    d1=c.flag_descriptor([flags],[])
    other=root/'other-headers';other.mkdir();(other/'reader.h').write_text('other input\n')
    flags.write_text('-idirafter '+str(other)+'\n')
    d2=c.flag_descriptor([flags],[])
    assert d1.footprint.digest!=d2.footprint.digest and d1.roots!=d2.roots
    other_file=other/'reader.h';previous=d2.footprint.digest;other_file.write_text('new contents same flags\n')
    assert c.flag_descriptor([flags],[]).footprint.digest!=previous
    ok('flags change registered root membership; unchanged flags plus changed descendant also invalidate')

    absent=root/'absent-include';flags.write_text('-I'+str(absent)+'\n')
    missing=c.flag_descriptor([flags],[]);assert missing.state=='Known' and str(absent) in missing.footprint.members
    absent.mkdir();(absent/'present.h').write_text('new input\n')
    assert c.flag_descriptor([flags],[]).footprint.digest!=missing.footprint.digest
    ok('source-supported absent include selector remains bound and addition changes identity')
    # Contained directory aliases are explicit input bindings, not silently omitted.
    aliased=root/'aliased-headers';aliased.mkdir();(aliased/'real').mkdir();(aliased/'real/value.h').write_text('aliased input\n')
    os.symlink(aliased/'real',aliased/'alias')
    fp,aliases=c.scoped_flag_tree((),(aliased,))
    assert str(aliased/'alias') in fp.members and str(aliased/'real/value.h') in fp.members
    assert aliases
    ok('declared contained directory alias binds both link and target bytes under limits')

    opaque=c.flag_descriptor([flags],['@unaccounted-response-file'])
    assert opaque.state=='Unknown'
    ok('opaque response/env readers remain Unknown rather than compiler-free normalization')

    # Loader metadata is a descriptor, not an unsupported ldd/global-closure claim.
    helper=root/'non-elf-helper';helper.write_bytes(b'opaque executable format\n')
    assert c.elf_descriptor(helper)['state']=='Unknown'
    ok('opaque helper is not certified by executable hash alone')

    # A minimal owned ELF fixture, not compiled/executed. Its DT_NEEDED edge is
    # parsed exactly like the installed response helper. Executable bytes stay equal
    # while a candidate dependency changes: binary-only approval would miss the read.
    import struct
    interp=root/'owned-loader';interp.write_text('owned loader binding, not executed\n')
    elf_file=root/'declared-helper';body=bytearray(4096)
    ident=b'\x7fELF\x02\x01\x01'+b'\0'*9
    body[:64]=struct.pack('<16sHHIQQQIHHHHHH',ident,2,62,1,0,64,0,0,64,56,3,0,0,0)
    programs=((1,4,0,0x400000,0x400000,4096,4096,4096),(2,4,256,0x400100,0x400100,64,64,8),(3,4,2048,0x400800,0x400800,len(str(interp).encode())+1,len(str(interp).encode())+1,1))
    for i,p in enumerate(programs):body[64+i*56:64+(i+1)*56]=struct.pack('<IIQQQQQQ',*p)
    strings=b'libfixture.so\0'
    for i,p in enumerate(((5,0x400c00),(10,len(strings)),(1,0),(0,0))):body[256+i*16:272+i*16]=struct.pack('<qQ',*p)
    body[2048:2048+len(str(interp).encode())+1]=str(interp).encode()+b'\0';body[3072:3072+len(strings)]=strings
    elf_file.write_bytes(body)
    libdir=root/'runtime-libs';libdir.mkdir();library=libdir/'libfixture.so';library.write_text('first runtime dependency\n')
    loader_cache=root/'loader-cache';loader_cache.write_text('owned lookup config\n')
    helper_env={'LD_LIBRARY_PATH':str(libdir)}
    a,parsed=c.helper_descriptor(elf_file,helper_env,(loader_cache,root/'absent-preload'))
    assert parsed['needed']==['libfixture.so'] and parsed['interpreter']==str(interp)
    helper_sha=hashlib.sha256(elf_file.read_bytes()).hexdigest();library.write_text('changed dependency with identical helper\n')
    b,_=c.helper_descriptor(elf_file,helper_env,(loader_cache,root/'absent-preload'))
    assert a.state==b.state=='Unknown' and a.footprint.digest!=b.footprint.digest
    assert helper_sha==hashlib.sha256(elf_file.read_bytes()).hexdigest()
    (CASE/'helper-runtime-falsifier.json').write_text(json.dumps({'unchangedHelperSha256':helper_sha,'needed':parsed['needed'],'interpreter':parsed['interpreter'],'oldDescriptor':a.report(),'newDescriptor':b.report(),'binaryOnlyWouldMiss':True,'runtimeClosureClaimed':False},indent=2)+'\n')
    ok('ELF-declared helper dependency falsifier invalidates identical executable; missing loader reader remains Unknown')

    # Secret values are internal only. Displayed identities use a session-private key.
    secret='DO-NOT-PRINT-OWNED-FIXTURE-SECRET'
    view=session.safe_environment(dict(env,GOFLAGS='-ldflags='+secret,ARBITRARY_AUTH_TOKEN=secret))
    assert secret not in json.dumps(view) and 'ARBITRARY_AUTH_TOKEN' not in view
    ok('whitelist secret-safe identities do not print arbitrary credentials or raw flags')

    (CASE/'source-receipt.json').write_text(json.dumps({'captureSourceSha256':hashlib.sha256((OUT/'capture.py').read_bytes()).hexdigest(),'testSourceSha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'actualRootScans':0,'actualGoArgv':[]},indent=2)+'\n')
    (CASE/'command-receipt.json').write_text(json.dumps({'kind':'injected owned metadata runner, NOT actual Go or quality','metadataCalls':calls,'proseGoFamilyArgv':[],'qualityOutcomes':[]},indent=2)+'\n')
    return results


class CaptureFixtureTests(unittest.TestCase):
    def test_owned_capture_controls(self):
        with tempfile.TemporaryDirectory(prefix='completion-capture-', dir=OUT) as temp:
            results = run_fixture(Path(temp) / 'case')
        failures = [item['control'] for item in results if not item['passed']]
        self.assertEqual(len(results), 17)
        self.assertEqual(failures, [])
