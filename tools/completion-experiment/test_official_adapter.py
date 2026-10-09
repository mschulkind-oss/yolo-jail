#!/bin/python3
"""Owned actual-adapter call-site controls; no Go argv or tool-file execution."""
import sys
sys.dont_write_bytecode=True
from pathlib import Path
import hashlib, json, os, types, tempfile, unittest
HERE=Path(__file__).resolve().parent
import capture
import closure
import official_adapter as adapter_module
from official_adapter import AcquisitionFailure, OfficialAdapter, ProviderReturn
from capture import FullCapture, FIELDS, Refused
from closure import Limits, Mutated, Unknown

def measure_tree_reads(root,callback):
    """Count bytes returned by reads of actual owned distribution files."""
    root=Path(root).absolute();counts={'total':0,'byPath':{},'calls':{}}
    original=Path.open
    class CountedReader:
        def __init__(self,stream,path):self.stream=stream;self.path=str(path)
        def __enter__(self):self.stream.__enter__();return self
        def __exit__(self,*args):return self.stream.__exit__(*args)
        def read(self,size=-1):
            data=self.stream.read(size)
            counts['total']+=len(data)
            counts['byPath'][self.path]=counts['byPath'].get(self.path,0)+len(data)
            counts['calls'][self.path]=counts['calls'].get(self.path,0)+1
            return data
        def __getattr__(self,name):return getattr(self.stream,name)
    def traced_open(path,mode='r',*args,**kwargs):
        stream=original(path,mode,*args,**kwargs)
        candidate=Path(os.path.abspath(path))
        if 'r' in mode and candidate.is_relative_to(root):return CountedReader(stream,candidate)
        return stream
    Path.open=traced_open
    try:outcome=callback()
    finally:Path.open=original
    return outcome,counts


def run_controls(case):
    CASE=Path(case)
    if not CASE.name or CASE.exists():
        raise AssertionError('owned fresh case directory required')
    CASE.mkdir()
    results=[]
    def record(name,callback):
        try:
            details=callback() or {}
            results.append(dict(control=name,passed=True,**details))
        except Exception as exc:
            results.append(dict(control=name,passed=False,error=type(exc).__name__+': '+str(exc)))

    def expect_code(code,callback):
        try:callback()
        except AcquisitionFailure as exc:
            assert exc.code==code,(exc.code,code)
            return exc.code
        raise AssertionError('expected acquisition status '+code)

    class FakeNormalFullProvider:
        """Owned normal-return fixture. Writes text stand-ins but never executes them."""
        def __init__(self,root,overrides=None,raw=None,exit_code=0,ready=True,version_exit=0,
                     version=None,extra_files=0,during=lambda:None,raise_after=False,
                     return_none=False,remove_binary=False,binary_bytes=None):
            self.root=Path(root);self.overrides=dict(overrides or {});self.raw=raw
            self.exit_code=exit_code;self.ready=ready;self.version_exit=version_exit
            self.version=version;self.extra_files=extra_files;self.during=during
            self.raise_after=raise_after;self.return_none=return_none;self.remove_binary=remove_binary
            self.binary_bytes=binary_bytes
            self.calls=0;self.requests=[]
        def __call__(self,request):
            self.calls+=1;self.requests.append(request)
            assert self.calls==1
            self.root.mkdir(parents=True)
            binary_text='fake Go stand-in; never executed' if self.binary_bytes is None else 'x'*self.binary_bytes
            files={'bin/go':binary_text,'VERSION':request.source_pin,
                   'pkg/tool/compile':'mutable compiler stand-in','src/input.go':'owned source'}
            for i in range(self.extra_files):files['extra/member-%04d.txt'%i]='owned member'
            for rel,data in files.items():
                p=self.root/rel;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(data)
            (self.root/'bin/go').chmod(0o755);(self.root/'pkg/tool/compile').chmod(0o755)
            self.during()
            if self.remove_binary:(self.root/'bin/go').unlink()
            if self.raise_after:raise RuntimeError('synthetic stderr secret must not escape')
            binary=str(self.root/'bin/go')
            raw=self.raw
            if raw is None:raw=json.dumps({'Dir':str(self.root),'Sum':'h1:'+'A'*43+'='}).encode()
            observed=dict(observed_context_identity=request.context_identity,
                observed_downloader_lexical=request.selected_downloader_lexical,
                observed_downloader_resolved=request.selected_downloader_resolved,
                observed_host_os=request.host_os,observed_host_arch=request.host_arch,
                observed_module=request.requested_module,download_json=raw,download_exit=self.exit_code,
                readiness_directory=str(self.root),readiness_succeeded=self.ready,
                version_binary=binary,version_output=self.version or ('go version '+request.source_pin+' '+request.host_os+'/'+request.host_arch),
                version_exit=self.version_exit,returned_binary=binary)
            observed.update(self.overrides)
            return None if self.return_none else ProviderReturn(**observed)


    def setup(name,provider_options=None,limits=Limits()):
        case=CASE/name;case.mkdir()
        goroot=case/'goroot';(goroot/'bin').mkdir(parents=True);(goroot/'pkg/tool').mkdir(parents=True)
        (goroot/'src').mkdir();(goroot/'bin/go').write_text('synthetic selected downloader; not executable')
        (goroot/'bin/go').chmod(0o755);(goroot/'VERSION').write_text('go1.26.7\n');(goroot/'go.env').write_text('CGO_CFLAGS=-O2 -g\n')
        home=case/'home';(home/'.config/go').mkdir(parents=True);settings=home/'.config/go/env'
        settings.write_text('CGO_CPPFLAGS=\n')
        repo=case/'repo';repo.mkdir();(repo/'go.mod').write_text('module owned.fixture\ngo 1.26.0\n');(repo/'go.sum').write_text('owned fixture sum\n')
        (repo/'Justfile').write_text('build:\n  go build ./...\n')
        env={'PATH':str(goroot/'bin'),'HOME':str(home),'GOENV':str(settings),'GOWORK':'off',
             'GOCACHE':str(case/'cache/go-build'),'GOMODCACHE':str(case/'cache/modules')}
        metadata={key:'' for key in FIELDS}
        metadata.update(GOENV=str(settings),GOROOT=str(goroot),GOTOOLDIR=str(goroot/'pkg/tool'),
            GOVERSION='go1.26.7',GOTOOLCHAIN='auto',GOOS='linux',GOARCH='amd64',GOHOSTOS='linux',GOHOSTARCH='amd64',
            GOMOD=str(repo/'go.mod'),GOWORK='off',GOCACHE=env['GOCACHE'],GOMODCACHE=env['GOMODCACHE'],
            CGO_ENABLED='1',CC='',CXX='')
        argv=[];env_seen=[]
        def runner(arguments,*,cwd,env):
            assert arguments[0]==str(goroot/'bin/go') and arguments[1:3]==['env','-json']
            assert arguments[3:]==list(FIELDS)
            argv.append(tuple(arguments));env_seen.append(dict(env))
            return dict(metadata,GOOS=env.get('GOOS','linux'))
        full=FullCapture(env,repo,runner=runner)
        before_env=dict(full.env);before_settings=settings.read_bytes()
        provider=FakeNormalFullProvider(case/'newly-returned-distribution',**(provider_options or {}))
        adapter=OfficialAdapter(full,provider,limits=limits)
        return adapter,provider,dict(env=before_env,settings=before_settings,settings_path=settings,
            argv=argv,env_seen=env_seen,repo=repo,goroot=goroot)


    def positive(name='positive'):
        obj,provider,state=setup(name)
        registrations=[]
        old=closure.Monitor
        class Traced(old):
            def register(self,fp):
                registrations.append(set(fp.members))
                super().register(fp)
        closure.Monitor=Traced
        try:
            def verify():
                assert provider.calls==1 and obj.acquisition_calls==1
                assert obj.phase=='verifying'
                assert str(provider.root/'pkg/tool/compile') in registrations[-1]
            snapshot=obj.capture_full(verify=verify)
        finally:closure.Monitor=old
        members=snapshot.footprint.members
        assert provider.calls==1
        assert str(provider.root/'src/input.go') in members
        assert str(provider.root/'extra/member-0000.txt') not in members
        assert snapshot.observation['protectedThroughFinalRereadAndDrain']
        assert snapshot.observation['prewatchEventsNotClaimed']
        assert snapshot.capture_snapshot.profile_state=='Unknown'
        assert snapshot.capture_snapshot.quality_outcomes==[]
        assert snapshot.capture_snapshot.report_data['baselinePublished'] is False
        command_count=len(obj.capture.commands)
        prose=obj.compare_prose(snapshot,obj.capture.env)
        assert prose['sameContext'] and prose['goFamilyArgv']==[]
        assert provider.calls==1 and len(obj.capture.commands)==command_count
        assert state['env']==obj.capture.env and state['settings']==state['settings_path'].read_bytes()
        assert obj.capture.env['HOME']==state['env']['HOME']
        assert obj.capture.env['GOCACHE']==state['env']['GOCACHE']
        return dict(providerCalls=provider.calls,acquisitionCalls=obj.acquisition_calls,
            fullMetadataRunnerCalls=len(state['argv']),proseGoFamilyArgv=prose['goFamilyArgv'],
            actualGoArgv=[],qualityOutcomes=[],profileState='Unknown',baselinePublished=False,
            returnedTreeRegisteredBeforeUse=True,checksumIsIndependentCacheIntegrityProof=False)

    record('actual new FULL adapter caller invokes one provider, learns/recaptures returned descendants before use; prose zero-acquisition',positive)


    def test_absence_falsifier():
        text=(HERE/'official_adapter.py').read_text()
        assert 'observed=self.provider(request)' in text
        assert 'combined=self._scope_from_base(base,acquired)' in text
        assert 'protected(discover,verify=dependent_use)' in text
        assert 'capture.official_descriptor' not in text
        return dict(callSite='OfficialAdapter.capture_full -> self.provider -> accept_return -> _scope_from_base -> protected -> verify',
            captureCallerDeletionWouldRemoveProviderAcquisition=True)
    record('new production-facing adapter caller is concrete and source-named',test_absence_falsifier)


    def deletion_mutants():
        source=(HERE/'official_adapter.py').read_text()
        substitutions={
          'delete-new-provider-call':('observed=self.provider(request)','observed=None'),
          'delete-returned-tree-read':('distribution=bounded_distribution_tree(root,self.limits,set(base.members),remaining_bytes)',
                                      "distribution=capture.Footprint({},'',0,0,0)"),
          'delete-protected-first-use':('footprint,observation=protected(discover,verify=dependent_use)','footprint=discover();observation={}'),
        }
        for name,(old,new) in substitutions.items():
            assert source.count(old)==1
            module=types.ModuleType('owned_adapter_mutant')
            module.__dict__.update({k:v for k,v in adapter_module.__dict__.items() if k not in ('__name__','__file__','__package__')})
            module.__file__=str(HERE/'official_adapter.py')
            sys.modules[module.__name__]=module
            exec(compile(source.replace(old,new),name,'exec'),module.__dict__)
            try:
                adapter_cls=module.OfficialAdapter
                obj,provider,state=setup('mutant-'+name)
                mutant=adapter_cls(obj.capture,provider)
                failed=False
                try:
                    snap=mutant.capture_full()
                    failed=(provider.calls!=1 or str(provider.root/'pkg/tool/compile') not in snap.footprint.members or
                            snap.observation.get('protectedThroughFinalRereadAndDrain') is not True)
                except (AssertionError,Unknown,Refused,AcquisitionFailure,AttributeError):failed=True
                assert failed,'deleting '+name+' escaped the adapter positive'
            finally:
                sys.modules.pop(module.__name__,None)
        return dict(mutants=list(substitutions),allLosePositive=True,mutatedProductionCaller='OfficialAdapter.capture_full')
    record('actual adapter provider/tree/protected call-site deletion mutants each lose positive',deletion_mutants)


    def failure(name,code,options=None):
        obj,provider,state=setup(name,options)
        dependent=[]
        expect_code(code,lambda:obj.capture_full(verify=lambda:dependent.append(True)))
        assert provider.calls==1 and not dependent and obj.snapshot is None
        return dict(status=code,providerCalls=provider.calls,dependentUse=False,actualGoArgv=[],profileState='Unknown')

    for label,code,options in (
     ('malformed-json','download_response_malformed',{'raw':b'{'}),
     ('duplicate-json-field','download_response_malformed',{'raw':b'{"Dir":"/one","Dir":"/two","Sum":"x"}'}),
     ('raw-response-too-large','download_response_too_large',{'raw':b'x'*(1024*1024+1)}),
     ('result-Error','download_result_error',{'raw':b'{"Error":"owned error"}'}),
     ('missing-Dir','download_missing_dir',{'raw':b'{"Sum":"h1:'+(b'A'*43)+b'="}'}),
     ('missing-Sum','download_missing_sum',{'raw':b'{"Dir":"/owned"}'}),
     ('nonzero-download','download_exit_nonzero',{'exit_code':2}),
     ('readiness-failure','readiness_failed',{'ready':False}),
     ('version-exit','version_exit_nonzero',{'version_exit':1}),
     ('wrong-version','version_mismatch',{'version':'go version go1.26.6 linux/amd64'}),
     ('wrong-host-version','version_mismatch',{'version':'go version go1.26.7 darwin/arm64'}),
     ('observed-host','actual_host_mismatch',{'overrides':{'observed_host_arch':'arm64'}}),
     ('observed-module','requested_module_mismatch',{'overrides':{'observed_module':'golang.org/toolchain@wrong'}}),
     ('observed-context','acquisition_context_mismatch',{'overrides':{'observed_context_identity':'other'}}),
     ('observed-downloader','observed_downloader_mismatch',{'overrides':{'observed_downloader_resolved':'/other/go'}}),
     ('observed-downloader-lexical','observed_downloader_mismatch',{'overrides':{'observed_downloader_lexical':'/other/go'}}),
     ('readiness-dir','readiness_dir_mismatch',{'overrides':{'readiness_directory':'/other/dir'}}),
     ('version-binary','returned_binary_mismatch',{'overrides':{'version_binary':'/other/go'}}),
     ('download-exit-bool','download_exit_nonzero',{'exit_code':True}),
     ('oversized-version','download_field_too_large',{'version':'x'*4097}),
     ('oversized-module','download_field_too_large',{'overrides':{'observed_module':'x'*513}}),
     ('missing-provider-return','provider_return_missing',{'return_none':True}),
    ):
        record(label+': exact bounded status; no fallback or dependent use',
            lambda label=label,code=code,options=options:failure('failure-'+label,code,options))


    def wrong_checksum():
        # Typed checksum validation remains the accepted official_descriptor's exact h1 constraint.
        obj,provider,state=setup('wrong-checksum',{'raw':None})
        provider.raw=json.dumps({'Dir':str(provider.root),'Sum':'not-a-checksum'}).encode()
        expect_code('official_result_invalid',obj.capture_full)
        return dict(status='official_result_invalid',providerCalls=1,dependentUse=False)
    record('invalid checksum is not treated as independent cache integrity proof',wrong_checksum)


    def version_newline():
        obj,provider,state=setup('version-single-newline',{'version':'go version go1.26.7 linux/amd64\n'})
        snap=obj.capture_full()
        assert snap.acquired.result.version_output.endswith('\n')
        return dict(singleCommandOutputNewlineAccepted=True,providerCalls=1)
    record('exact version line accepts one command-output newline',version_newline)


    def version_extra_whitespace():
        obj,provider,state=setup('version-extra-whitespace',{'version':' go version go1.26.7 linux/amd64\n'})
        expect_code('version_mismatch',obj.capture_full)
        return dict(status='version_mismatch',extraLeadingWhitespaceRejected=True)
    record('exact version binding rejects extra whitespace',version_extra_whitespace)


    def nonexec():
        obj,provider,state=setup('absent-returned-go',{'remove_binary':True})
        expect_code('returned_binary_unavailable',obj.capture_full)
        return dict(status='returned_binary_unavailable',dependentUse=False)
    record('absent selected returned binary refuses readiness',nonexec)


    def nonexecutable_mode():
        obj,provider,state=setup('nonexecutable-returned-go')
        provider.during=lambda:(provider.root/'bin/go').chmod(0o644)
        expect_code('returned_binary_unavailable',obj.capture_full)
        return dict(status='returned_binary_unavailable',dependentUse=False)
    record('nonexecutable selected returned binary refuses readiness',nonexecutable_mode)


    def provider_exception():
        obj,provider,state=setup('provider-exception',{'raise_after':True})
        try:obj.capture_full()
        except AcquisitionFailure as exc:
            assert exc.code=='provider_failed' and 'secret' not in str(exc)
            assert exc.__suppress_context__ is True and provider.calls==1 and obj.snapshot is None
            return dict(status=exc.code,rawProviderErrorRedacted=True,dependentUse=False)
        raise AssertionError('normal provider failure accepted')
    record('normal provider exception propagates without successful partial acquisition',provider_exception)


    def route_guard():
        obj,provider,state=setup('route-guard')
        obj.route='prose'
        try:obj.capture_full()
        except Refused:
            assert provider.calls==0 and not state['argv']
            return dict(providerCalls=0,metadataRunnerCalls=0)
        raise AssertionError('non-FULL reached provider or metadata')
    record('non-FULL route refuses before metadata/provider invocation',route_guard)


    def exact_response_bound():
        raw=json.dumps({'Dir':'/owned/path','Sum':'h1:'+'A'*43+'='}).encode()
        raw+=b' '*(1024*1024-len(raw))
        assert len(raw)==1024*1024
        obj,provider,state=setup('one-mib-response',{'raw':raw})
        # The normal root path is checked by actual result handling; use a valid owned path in provider JSON.
        provider.raw=json.dumps({'Dir':str(provider.root),'Sum':'h1:'+'A'*43+'='}).encode()
        provider.raw+=b' '*(1024*1024-len(provider.raw))
        snap=obj.capture_full()
        assert snap.acquired.result.checksum=='h1:'+'A'*43+'='
        return dict(rawBytes=1024*1024,acceptedAtInclusiveLimit=True,providerCalls=1)
    record('download response bound is inclusive at exactly one MiB',exact_response_bound)


    def oversized_directory():
        obj,provider,state=setup('oversized-dir-field')
        provider.raw=json.dumps({'Dir':'/'+('d'*4096),'Sum':'h1:'+'A'*43+'='}).encode()
        expect_code('download_field_too_large',obj.capture_full)
        return dict(status='download_field_too_large',partialResultRejected=True)
    record('actual Dir field bound rejects oversized raw path before traversal',oversized_directory)


    def oversized_checksum():
        obj,provider,state=setup('oversized-checksum-field')
        provider.raw=json.dumps({'Dir':str(provider.root),'Sum':'x'*65}).encode()
        expect_code('download_field_too_large',obj.capture_full)
        return dict(status='download_field_too_large',partialResultRejected=True)
    record('actual checksum field bound enforced without certifying integrity',oversized_checksum)


    def request_replacement():
        obj,provider,state=setup('late-return')
        def verify():
            obj.accept_return(provider.requests[0],ProviderReturn('x','','','','','',b'{}'))
        expect_code('late_result_rejected',lambda:obj.capture_full(verify=verify))
        assert provider.calls==1 and obj.snapshot is None
        return dict(status='late_result_rejected',dependentUse=False)
    record('late provider result replacement is rejected after first-use barrier',request_replacement)


    def restored_context_failure():
        obj,provider,state=setup('restored-context-provider-failure')
        def write_restore():
            p=state['settings_path'];old=p.read_bytes();p.write_bytes(b'CGO_CPPFLAGS=-I/temporary\n');p.write_bytes(old)
        provider.during=write_restore
        provider.raise_after=True
        try:obj.capture_full()
        except Mutated:return dict(outcome='Mutated',contextBytesRestored=True,providerCalls=1)
        raise AssertionError('acquisition error discarded an already observed context mutation')
    record('already watched restored context mutation outranks simultaneous provider error',restored_context_failure)


    def restored_distribution_write():
        obj,provider,state=setup('distribution-restored-write')
        def verify():
            p=provider.root/'pkg/tool/compile';old=p.read_bytes();p.write_bytes(b'temporary');p.write_bytes(old)
        try:obj.capture_full(verify=verify)
        except Mutated:return dict(outcome='Mutated',endpointBytesRestored=True)
        raise AssertionError('decoded distribution mutation was cleared/ignored')
    record('acquired distribution restored write remains fatal during protected verification',restored_distribution_write)


    def new_member_during_verify():
        obj,provider,state=setup('distribution-new-member')
        try:obj.capture_full(verify=lambda:(provider.root/'late.txt').write_text('late'))
        except Mutated:return dict(outcome='Mutated',lateMember=True)
        raise AssertionError('late distribution member replaced protected acquisition')
    record('late distribution member cannot replace acquired returned tree',new_member_during_verify)


    def prose_changed():
        obj,provider,state=setup('prose-changed')
        snap=obj.capture_full();p=provider.root/'pkg/tool/compile';p.write_text('changed bytes')
        expect_code('prose_context_changed',lambda:obj.compare_prose(snap,obj.capture.env))
        assert provider.calls==1
        return dict(providerCalls=1,proseGoFamilyArgv=[],status='prose_context_changed')
    record('changed actual distribution descendant invalidates prose without reacquisition',prose_changed)


    def imported_snapshot():
        obj,provider,state=setup('imported-snapshot');snap=obj.capture_full()
        other,unused,other_state=setup('other-session')
        try:other.compare_prose(snap,other.capture.env)
        except Refused:return dict(providerCalls=1,importedSnapshotRejected=True,proseGoFamilyArgv=[])
        raise AssertionError('another session accepted imported snapshot')
    record('only this session protected acquired snapshot can enter prose',imported_snapshot)


    def combined_bytes_budget():
        obj,provider,state=setup('combined-bytes',{'extra_files':4})
        obj.limits=Limits(entries=65536,bytes=2048)
        # Give actual returned members enough bytes to exceed the total context+tree budget.
        provider.extra_files=4
        original=provider.__call__
        class LargeProvider:
            calls=0
            def __call__(self,request):
                result=original(request);self.calls=provider.calls
                for p in provider.root.glob('extra/*'):p.write_text('x'*1024)
                return result
        obj.provider=LargeProvider()
        try:obj.capture_full()
        except AcquisitionFailure as exc:
            assert exc.code in ('distribution_scope_unknown','combined_scope_budget')
            return dict(status=exc.code,partialDistributionAccepted=False)
        raise AssertionError('combined actual streamed bytes exceeded budget')
    record('combined actual context/distribution streamed-byte budget fails closed',combined_bytes_budget)


    def entry_budget():
        obj,provider,state=setup('combined-entries',{'extra_files':40},limits=Limits(entries=10,bytes=768*1024*1024))
        try:obj.capture_full()
        except (AcquisitionFailure,Unknown):return dict(partialDistributionAccepted=False,actualGoArgv=[])
        raise AssertionError('combined context/distribution membership exceeded budget')
    record('combined actual context/distribution membership bound fails closed',entry_budget)


    def unreadable_enumeration():
        obj,provider,state=setup('unreadable-distribution')
        old=adapter_module.os.scandir
        def failed(path):
            if Path(path).is_relative_to(provider.root):raise PermissionError('owned unreadable descendant')
            return old(path)
        adapter_module.os.scandir=failed
        try:
            try:obj.capture_full()
            except AcquisitionFailure as exc:
                assert exc.code=='distribution_scope_unknown'
                return dict(status=exc.code,partialDistributionAccepted=False)
            raise AssertionError('unreadable returned descendant accepted')
        finally:adapter_module.os.scandir=old
    record('unreadable actual distribution descendant rejects partial tree',unreadable_enumeration)


    def symlink_escape():
        obj,provider,state=setup('distribution-link-escape')
        outside=CASE/'outside-tree';outside.mkdir();(outside/'secret').write_text('owned synthetic external link target')
        def add_link():os.symlink(outside,provider.root/'external')
        provider.during=add_link
        try:obj.capture_full()
        except AcquisitionFailure as exc:
            assert exc.code=='distribution_scope_unknown'
            return dict(status=exc.code,unsupportedAliasRejected=True)
        raise AssertionError('unsupported directory alias accepted')
    record('unsupported actual distribution alias fails closed',symlink_escape)


    def zero_real_commands_and_privacy():
        obj,provider,state=setup('privacy-and-environment');original_env=dict(obj.capture.env)
        before_settings=state['settings_path'].read_bytes();snap=obj.capture_full()
        shown=json.dumps(snap.capture_snapshot.report_data,sort_keys=True)
        assert str(provider.root) not in shown and 'h1:' not in shown
        assert obj.capture.env==original_env and state['settings_path'].read_bytes()==before_settings
        assert snap.capture_snapshot.report_data['profileState']=='Unknown'
        assert snap.capture_snapshot.report_data['qualityOutcomes']==[]
        assert snap.capture_snapshot.report_data['baselinePublished'] is False
        return dict(actualGoArgv=[],syntheticMetadataRunner=True,environmentAndHOMEUnchanged=True,
            defaultCacheChoicesUnchanged=True,rawDownloadAndChecksumNotReported=True,
            qualityOutcomes=[],profileState='Unknown',baselinePublished=False)
    record('privacy keyed metadata and ordinary HOME/cache/environment remain unchanged; no quality/provenance claim',zero_real_commands_and_privacy)

    def _capture_outcome(callback):
        try:callback()
        except AcquisitionFailure as exc:return exc.code
        return 'accepted'


    def _prose_outcome(obj,snapshot):
        try:obj.compare_prose(snapshot,obj.capture.env)
        except AcquisitionFailure as exc:return exc.code
        return 'accepted'


    def error_field_types():
        false_values=(False,0,[],{},None)
        accepted=[];statuses=[]
        for index,value in enumerate(false_values):
            obj,provider,state=setup('malformed-error-type-%d'%index)
            provider.raw=json.dumps({'Dir':str(provider.root),'Sum':'h1:'+'A'*43+'=','Error':value}).encode()
            try:obj.capture_full()
            except AcquisitionFailure as exc:statuses.append(exc.code)
            else:accepted.append(type(value).__name__)
        assert not accepted,'false-valued non-string Error fields accepted: '+','.join(accepted)
        assert statuses==['download_error_invalid']*len(false_values),statuses
        obj,provider,state=setup('nonempty-error-redaction')
        secret='PRIVATE-DOWNLOAD-ERROR-CONTENT'
        provider.raw=json.dumps({'Dir':str(provider.root),'Sum':'h1:'+'A'*43+'=','Error':secret}).encode()
        try:obj.capture_full()
        except AcquisitionFailure as exc:
            assert exc.code=='download_result_error' and secret not in str(exc)
        else:raise AssertionError('nonempty Go Error accepted')
        return dict(rejectedTypes=['false','zero','array','object','null'],statuses=statuses,
            nonemptyStringRejected=True,rawErrorPrinted=False)
    record('download Error accepts only string/omitted fields; malformed JSON types reject',error_field_types)


    def empty_error_string():
        obj,provider,state=setup('empty-error-string')
        provider.raw=json.dumps({'Dir':str(provider.root),'Sum':'h1:'+'A'*43+'=','Error':''}).encode()
        snapshot=obj.capture_full()
        assert snapshot.acquired.result.directory==str(provider.root)
        return dict(emptyStringAccepted=True,providerCalls=1)
    record('empty optional Go download Error string remains a valid result',empty_error_string)


    def bounded_acquisition_read():
        obj,provider,state=setup('bounded-acquisition-read',{'binary_bytes':64*1024})
        _,context,request=obj._metadata_and_context(obj.capture.env)
        allowance=1024
        obj.limits=Limits(entries=65536,bytes=context.bytes+allowance)
        outcome,reads=measure_tree_reads(provider.root,lambda:_capture_outcome(obj.capture_full))
        binary=str(provider.root/'bin/go')
        assert outcome in ('distribution_scope_unknown','combined_scope_budget'),outcome
        assert reads['byPath'].get(binary,0)>0,'returned bin/go was never actually read'
        assert reads['byPath'].get(str(provider.root/'VERSION'))==len(obj.capture.pin.encode())
        assert reads['total']==allowance+1
        assert reads['byPath'][binary]==allowance+1-reads['byPath'][str(provider.root/'VERSION')]
        assert reads['total']<=allowance+1,('read exceeded remaining budget',reads,allowance)
        return dict(contextBytes=context.bytes,remainingBytes=allowance,
            returnedBinaryBytesRead=reads['byPath'][binary],returnedReadAttempted=True,
            versionBytesRead=reads['byPath'].get(str(provider.root/'VERSION'),0),
            totalActualReturnedBytes=reads['total'],maximumAllowedActualBytes=allowance+1,
            status=outcome)
    record('64 KiB returned bin/go is actually attempted but capped at measured context remainder plus one',bounded_acquisition_read)


    def bounded_prose_read():
        obj,provider,state=setup('bounded-prose-read')
        snapshot=obj.capture_full()
        context_bytes=obj._last_context.bytes
        allowance=32
        obj.limits=Limits(entries=65536,bytes=context_bytes+allowance)
        outcome,reads=measure_tree_reads(provider.root,
            lambda:_prose_outcome(obj,snapshot))
        binary=str(provider.root/'bin/go')
        assert outcome in ('distribution_scope_unknown','combined_scope_budget'),outcome
        assert reads['byPath'].get(binary,0)>0,'prose recapture never read returned bin/go'
        assert reads['byPath'].get(str(provider.root/'VERSION'))==len(obj.capture.pin.encode())
        assert reads['total']==allowance+1
        assert reads['byPath'][binary]==allowance+1-reads['byPath'][str(provider.root/'VERSION')]
        assert reads['total']<=allowance+1,('prose recapture exceeded remaining budget',reads,allowance)
        return dict(contextBytes=context_bytes,remainingBytes=allowance,
            returnedBinaryBytesRead=reads['byPath'][binary],returnedReadAttempted=True,
            versionBytesRead=reads['byPath'].get(str(provider.root/'VERSION'),0),
            proseActualReturnedBytes=reads['total'],maximumAllowedActualBytes=allowance+1,
            status=outcome,goFamilyArgv=[])
    record('prose recapture reads the actual returned tree only within remaining budget plus one',bounded_prose_read)
    receipt=dict(results=results,passed=sum(x['passed'] for x in results),total=len(results),
        actualGoArgv=[],metadataRunner='owned injected FullCapture runner; no tool execution',
        provider='owned fake normal-full callback; no synthetic file executed',
        qualityOutcomes=[],profileState='Unknown',baselinePublished=False,
        sources={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in
           (HERE/'official_adapter.py',Path(__file__),HERE/'capture.py',HERE/'test_capture.py',HERE/'test_capture_semantics.py')})
    (CASE/'results.json').write_text(json.dumps(receipt,indent=2)+'\n')
    return results


class OfficialAdapterFixtureTests(unittest.TestCase):
    def test_owned_adapter_controls(self):
        with tempfile.TemporaryDirectory(prefix='completion-adapter-', dir=HERE) as temp:
            results = run_controls(Path(temp) / 'case')
        failures = [item['control'] for item in results if not item['passed']]
        self.assertGreater(len(results), 30)
        self.assertEqual(failures, [])
