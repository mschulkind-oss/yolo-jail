#!/bin/python3
"""One-shot normal-FULL official toolchain return adapter.

This private adapter composes the accepted FullCapture readers with the existing
protected observer. Its provider is the normal fetchToolchain return seam; it must
return before the caller's buildOne. The module never launches a tool itself.
"""
import sys
sys.dont_write_bytecode=True
from dataclasses import dataclass, field
from pathlib import Path
import hashlib, json, os, re, stat
import capture
from closure import Limits, Unknown, protected

RAW_DOWNLOAD_LIMIT=1024*1024
FIELD_LIMITS={'module':512,'directory':4096,'checksum':64,'version_output':4096}

class AcquisitionFailure(Unknown):
    def __init__(self, code, message):
        self.code=code
        super().__init__(message)

@dataclass(frozen=True)
class AcquisitionRequest:
    context_identity:str
    selected_downloader_lexical:str
    selected_downloader_resolved:str
    host_os:str
    host_arch:str
    source_pin:str
    requested_module:str

@dataclass(frozen=True)
class ProviderReturn:
    """In-memory observations from the ordinary fetchToolchain call path."""
    observed_context_identity:str
    observed_downloader_lexical:str
    observed_downloader_resolved:str
    observed_host_os:str
    observed_host_arch:str
    observed_module:str
    download_json:bytes|str=field(repr=False)
    download_exit:int=0
    readiness_directory:str=''
    readiness_succeeded:bool=False
    version_binary:str=''
    version_output:str=field(default='',repr=False)
    version_exit:int=0
    returned_binary:str=''

@dataclass(frozen=True)
class AcquiredOfficial:
    request:AcquisitionRequest
    result:capture.OfficialResult
    returned_binary:str

@dataclass(frozen=True)
class AdapterSnapshot:
    capture_snapshot:capture.Snapshot
    acquired:AcquiredOfficial
    footprint:object
    observation:dict


def _fail(code,message):
    raise AcquisitionFailure(code,message)


def _raw_json(raw):
    if isinstance(raw,bytes):
        if len(raw)>RAW_DOWNLOAD_LIMIT:_fail('download_response_too_large','Download JSON exceeds the 1 MiB response bound')
        try:text=raw.decode('utf-8')
        except UnicodeError:_fail('download_response_malformed','Download JSON is not UTF-8')
    elif isinstance(raw,str):
        try:encoded=raw.encode('utf-8')
        except UnicodeError:_fail('download_response_malformed','Download JSON is not UTF-8')
        if len(encoded)>RAW_DOWNLOAD_LIMIT:_fail('download_response_too_large','Download JSON exceeds the 1 MiB response bound')
        text=raw
    else:_fail('download_response_malformed','Download response is not bounded text')
    def unique(pairs):
        result={}
        for key,value in pairs:
            if key in result:raise ValueError('duplicate JSON field')
            result[key]=value
        return result
    try:info=json.loads(text,object_pairs_hook=unique)
    except (ValueError,TypeError):_fail('download_response_malformed','Download JSON is malformed')
    if not isinstance(info,dict):_fail('download_response_malformed','Download JSON result is not an object')
    if 'Error' in info:
        error=info['Error']
        if not isinstance(error,str):
            _fail('download_error_invalid','Download JSON Error field must be a string when present')
        if error!='':_fail('download_result_error','Go module downloader returned an Error')
    if not isinstance(info.get('Dir'),str):_fail('download_missing_dir','Download result has no actual Dir')
    if not isinstance(info.get('Sum'),str):_fail('download_missing_sum','Download result has no actual Sum')
    return info


def _bounded_field(name,value):
    maximum=FIELD_LIMITS[name]
    if not isinstance(value,str):_fail('download_field_invalid','Observed '+name+' field has the wrong type')
    try:size=len(value.encode('utf-8'))
    except UnicodeError:_fail('download_field_invalid','Observed '+name+' field is not UTF-8')
    if size>maximum:_fail('download_field_too_large','Observed '+name+' field exceeds its bound')
    return value


def bounded_distribution_tree(root,limits,existing_members,byte_limit):
    """Read a returned tree once, streaming no more than byte_limit plus one."""
    root=Path(os.path.abspath(root));canonical_root=root.resolve(strict=True)
    members={};processed=set();file_paths=set();directory_paths=set();total_bytes=0

    def add(path,value):
        name=str(path)
        if name not in members and name not in existing_members and len(existing_members)+len(members)>=limits.entries:
            raise Unknown('combined input membership exceeds bounded entry budget')
        members[name]=value

    def selectors(path):
        for candidate in (path,*path.parents):
            try:st=candidate.lstat()
            except FileNotFoundError:continue
            if stat.S_ISLNK(st.st_mode):
                add(candidate,'link:'+str((st.st_dev,st.st_ino,st.st_mode))+':'+os.readlink(candidate))

    def one(path):
        nonlocal total_bytes
        path=Path(os.path.abspath(path));selectors(path);name=str(path)
        if name in processed:return
        try:st=path.lstat()
        except FileNotFoundError:
            add(path,'absent');processed.add(name);return
        if stat.S_ISLNK(st.st_mode):
            target=path.resolve(strict=True)
            add(path,'link:'+str((st.st_dev,st.st_ino,st.st_mode))+':'+os.readlink(path))
            if not target.is_relative_to(canonical_root):
                raise Unknown('returned distribution link leaves its selected root')
            target_stat=target.stat()
            if stat.S_ISDIR(target_stat.st_mode):
                raise Unknown('returned distribution directory alias is unsupported')
            if not stat.S_ISREG(target_stat.st_mode):
                raise Unknown('returned distribution alias target type is unsupported')
            processed.add(name)
            one(target)
            return
        if stat.S_ISDIR(st.st_mode):
            add(path,'dir:'+str((st.st_dev,st.st_ino,st.st_mode)))
            directory_paths.add(name);processed.add(name)
            with os.scandir(path) as entries:
                for entry in sorted(entries,key=lambda item:item.name):
                    one(Path(entry.path))
            return
        if not stat.S_ISREG(st.st_mode):
            raise Unknown('unsupported returned distribution member type')
        if name in file_paths:return
        if name not in existing_members and len(existing_members)+len(members)>=limits.entries:
            raise Unknown('combined input membership exceeds bounded entry budget')
        digest=hashlib.sha256();read_bytes=0
        with path.open('rb') as stream:
            while True:
                chunk=stream.read(min(1024*1024,byte_limit-total_bytes+1))
                if not chunk:break
                amount=len(chunk);total_bytes+=amount;read_bytes+=amount
                if total_bytes>byte_limit:
                    raise Unknown('actual returned distribution bytes exceed remaining combined budget')
                digest.update(chunk)
        add(path,'file:'+str((st.st_dev,st.st_ino,st.st_mode,read_bytes))+':'+digest.hexdigest())
        file_paths.add(name);processed.add(name)

    if not root.is_dir():raise Unknown('actual returned distribution root is not a directory')
    one(root)
    digest=hashlib.sha256(json.dumps(sorted(members.items()),separators=(',',':')).encode()).hexdigest()
    return capture.Footprint(members,digest,len(file_paths),len(directory_paths),total_bytes)


def _validate_acquired(acquired):
    request,result=acquired.request,acquired.result
    expected_module='golang.org/toolchain@v0.0.1-'+request.source_pin+'.'+request.host_os+'-'+request.host_arch
    if (not isinstance(result,capture.OfficialResult) or result.module!=expected_module or
        result.module!=request.requested_module or type(result.download_exit) is not int or result.download_exit!=0 or
        type(result.version_exit) is not int or result.version_exit!=0 or result.ready is not True or
        not isinstance(result.checksum,str) or not re.fullmatch(r'h1:[A-Za-z0-9+/]{43}=',result.checksum) or
        not isinstance(result.directory,str) or len(result.directory.encode())>FIELD_LIMITS['directory'] or
        not Path(result.directory).is_absolute() or '\x00' in result.directory):
        _fail('official_result_invalid','Typed normal-full result no longer matches source pin and host')
    version=_bounded_field('version_output',result.version_output)
    expected='go version '+request.source_pin+' '+request.host_os+'/'+request.host_arch
    actual=version[:-1] if version.endswith('\n') else version
    if actual!=expected or '\n' in actual or '\r' in actual:
        _fail('version_mismatch','Chosen downloaded Go output differs from exact source pin/host')
    expected_binary=str(Path(result.directory)/'bin/go')
    if acquired.returned_binary!=expected_binary:
        _fail('returned_binary_mismatch','fetchToolchain return no longer names actual Dir/bin/go')
    try:
        if not Path(result.directory).is_dir():
            _fail('distribution_unavailable','Actual returned distribution Dir is unavailable')
        binary_stat=Path(expected_binary).stat()
    except OSError:
        _fail('returned_binary_unavailable','Actual returned bin/go metadata is unavailable')
    if not stat.S_ISREG(binary_stat.st_mode) or not os.access(expected_binary,os.X_OK):
        _fail('returned_binary_unavailable','Actual returned bin/go is absent or nonexecutable')
    return Path(result.directory)


class OfficialAdapter:
    """Actual FULL caller around FullCapture metadata/descriptor interfaces."""
    def __init__(self,full_capture,normal_full_provider,route='full',limits=Limits()):
        self.capture=full_capture
        self.provider=normal_full_provider
        self.route=route
        self.limits=limits
        self.phase='idle'
        self.acquisition_calls=0
        self.acquired=None
        self.snapshot=None
        self._candidate=None
        self._last_metadata=None
        self._last_context=None

    def _request_for(self,metadata,base):
        host=metadata['base']
        pin=self.capture.pin
        module='golang.org/toolchain@v0.0.1-'+pin+'.'+host['GOHOSTOS']+'-'+host['GOHOSTARCH']
        binding={'footprint':base.digest,'lexical':str(self.capture.lexical_go),
                 'resolved':str(self.capture.go),'pin':pin,'goos':host['GOHOSTOS'],
                 'goarch':host['GOHOSTARCH'],'module':module}
        token=self.capture.opaque(json.dumps(binding,sort_keys=True,separators=(',',':')))
        request=AcquisitionRequest(token,str(self.capture.lexical_go),str(self.capture.go),
                                   host['GOHOSTOS'],host['GOHOSTARCH'],pin,module)
        return request

    def _metadata_and_context(self,env):
        metadata={name:self.capture.metadata('full',target) for name,target in
                  (('base',None),('lint-linux','linux'),('lint-darwin','darwin'))}
        base=self.capture.discover(metadata,env,None)
        return metadata,base,self._request_for(metadata,base)


    def _read_provider_return(self,request,observed):
        if not isinstance(observed,ProviderReturn):_fail('provider_return_missing','Normal full did not return typed command observations')
        if observed.observed_context_identity!=request.context_identity:
            _fail('acquisition_context_mismatch','Normal-full command observations bind another context')
        if (observed.observed_downloader_lexical!=request.selected_downloader_lexical or
            observed.observed_downloader_resolved!=request.selected_downloader_resolved):
            _fail('observed_downloader_mismatch','Observed command downloader differs from selected PATH Go')
        if (observed.observed_host_os!=request.host_os or observed.observed_host_arch!=request.host_arch):
            _fail('actual_host_mismatch','Observed toolchain target differs from the runtime host')
        observed_module=_bounded_field('module',observed.observed_module)
        if observed_module!=request.requested_module:
            _fail('requested_module_mismatch','Observed requested module differs from the pinned host module')
        if type(observed.download_exit) is not int or observed.download_exit!=0:
            _fail('download_exit_nonzero','Normal-full module download exited unsuccessfully')
        info=_raw_json(observed.download_json)
        directory=_bounded_field('directory',info['Dir'])
        checksum=_bounded_field('checksum',info['Sum'])
        if not Path(directory).is_absolute() or '\x00' in directory:
            _fail('download_dir_invalid','Actual returned Dir must be a valid absolute path')
        if type(observed.readiness_succeeded) is not bool or not observed.readiness_succeeded:
            _fail('readiness_failed','Normal-full allowExec readiness did not succeed')
        if observed.readiness_directory!=directory:
            _fail('readiness_dir_mismatch','Readiness did not bind the actual downloaded Dir')
        expected_binary=str(Path(directory)/'bin/go')
        if observed.version_binary!=expected_binary or observed.returned_binary!=expected_binary:
            _fail('returned_binary_mismatch','Version and fetchToolchain return must name actual Dir/bin/go')
        version=_bounded_field('version_output',observed.version_output)
        if type(observed.version_exit) is not int or observed.version_exit!=0:
            _fail('version_exit_nonzero','Chosen downloaded Go version command failed')
        expected_version='go version '+request.source_pin+' '+request.host_os+'/'+request.host_arch
        actual_version=version[:-1] if version.endswith('\n') else version
        if actual_version!=expected_version or '\n' in actual_version or '\r' in actual_version:
            _fail('version_mismatch','Chosen downloaded Go output differs from exact source pin/host')
        if not Path(expected_binary).is_file() or not os.access(expected_binary,os.X_OK):
            _fail('returned_binary_unavailable','Actual returned bin/go is absent or nonexecutable')
        result=capture.OfficialResult(request.requested_module,directory,checksum,
            observed.download_exit,observed.readiness_succeeded,version,observed.version_exit)
        acquired=AcquiredOfficial(request,result,expected_binary)
        _validate_acquired(acquired)
        return acquired

    def _scope_from_base(self,base,acquired):
        root=_validate_acquired(acquired)
        remaining_entries=self.limits.entries-len(base.members)
        remaining_bytes=self.limits.bytes-base.bytes
        if remaining_entries<=0 or remaining_bytes<0:
            _fail('combined_scope_budget','Context exhausts combined distribution entries/bytes budget')
        try:
            distribution=bounded_distribution_tree(root,self.limits,set(base.members),remaining_bytes)
            combined=capture.merge_footprints((base,distribution))
        except (Unknown,OSError,ValueError,RecursionError):
            raise AcquisitionFailure('distribution_scope_unknown',
                'Actual returned distribution descendants are not fully readable within bounds') from None
        if len(combined.members)>self.limits.entries or base.bytes+distribution.bytes>self.limits.bytes:
            _fail('combined_scope_budget','Context plus actual distribution exceeds combined entries/bytes budget')
        result_binding=json.dumps({'base':base.digest,'request':acquired.request.__dict__,
            'result':acquired.result.__dict__,'returned_binary':acquired.returned_binary},
            sort_keys=True,separators=(',',':'))
        combined.digest=self.capture.opaque(combined.digest+result_binding)
        return combined

    def _captured_scope(self,env,expected_request):
        metadata,base,request=self._metadata_and_context(env)
        if request!=expected_request:
            _fail('acquisition_context_changed','Context/source/downloader changed during normal-full acquisition')
        acquired=self.acquired
        if acquired is None or acquired.request!=request:
            _fail('acquisition_result_missing','No protected result from this FULL session')
        combined=self._scope_from_base(base,acquired)
        self._last_metadata=metadata
        self._last_context=base
        return combined

    def accept_return(self,request,observed):
        if self.phase!='acquiring' or self.acquired is not None or self.acquisition_calls!=1:
            _fail('late_result_rejected','Official result is one-shot and accepted only at the FULL return seam')
        acquired=self._read_provider_return(request,observed)
        if acquired.request!=request:_fail('request_binding_mismatch','Acquired result did not retain the private request')
        self.acquired=acquired
        return acquired

    def capture_full(self,verify=lambda:None):
        if self.route!='full':raise capture.Refused('Official acquisition adapter is FULL-only')
        if self.phase!='idle':raise capture.Refused('Normal-full official acquisition is one-shot')
        self.phase='bootstrap'
        def discover():
            metadata,base,request=self._metadata_and_context(self.capture.env)
            if self.acquired is None:
                if self._candidate!=request:
                    self._candidate=request
                    self._last_metadata=metadata
                    self._last_context=base
                    return base
                self.acquisition_calls+=1
                if self.acquisition_calls!=1:_fail('acquisition_called_twice','Normal-full provider may run exactly once')
                self.phase='acquiring'
                try:observed=self.provider(request)
                except Exception:
                    self.phase='bootstrap'
                    raise AcquisitionFailure('provider_failed',
                        'Normal-full toolchain acquisition failed; retry ordinary full') from None
                self.accept_return(request,observed)
                self.phase='bootstrap'
                # The newly returned directory is provisional until protected()
                # registers it and performs complete member/content rereads.
                return self._captured_scope(self.capture.env,request)
            return self._captured_scope(self.capture.env,self.acquired.request)

        def dependent_use():
            if self.acquired is None or self.acquisition_calls!=1:
                _fail('acquisition_not_ready','Dependent use requires this FULL session return')
            self.phase='verifying'
            verify()
        try:
            footprint,observation=protected(discover,verify=dependent_use)
        except Exception:
            self.phase='failed'
            raise
        if self.acquired is None or self.phase!='verifying':
            self.phase='failed'
            _fail('acquisition_not_ready','Protected normal-full acquisition did not complete')
        report=dict(scope='protected normal-FULL official adapter snapshot; not quality/provenance',
            metadataState='Known',profileState='Unknown',officialState='Known',
            officialResultState='normal-full observed Dir/Sum/readiness/version; session-local only',
            checksumIsIndependentCacheIntegrityProof=False,
            distributionScope=dict(files=footprint.files,directories=footprint.directories,
                                   bytesHashed=footprint.bytes,membershipCount=len(footprint.members)),
            identity=self.capture.safe_footprint(footprint),observation=observation,
            commandCount=len(self.capture.commands),qualityOutcomes=[],baselinePublished=False,
            contextLimitations=['helper/GCC/Node/Pi runtime reads and whole Go provenance are not claimed',
                'prewatch restored writes and atomic-publication protection are not claimed'])
        saved=capture.Snapshot('Known','Unknown','Known',footprint,dict(self._last_metadata),report,[])
        saved.official=self.acquired.result
        self.snapshot=AdapterSnapshot(saved,self.acquired,footprint,observation)
        self.phase='complete'
        return self.snapshot

    def compare_prose(self,snapshot,env,verify=lambda:None):
        if snapshot is not self.snapshot or self.phase!='complete' or snapshot.acquired is not self.acquired:
            raise capture.Refused('Prose requires this adapter session’s protected acquired snapshot')
        count=len(self.capture.commands)
        def reread():
            base=self.capture.discover(snapshot.capture_snapshot.raw_metadata,dict(env),None)
            request=self._request_for(snapshot.capture_snapshot.raw_metadata,base)
            if request!=snapshot.acquired.request:
                _fail('prose_context_changed','Captured context/request binding changed')
            return self._scope_from_base(base,snapshot.acquired)
        current,observation=protected(reread,verify=verify)
        if len(self.capture.commands)!=count:raise AssertionError('Prose route reached Go metadata')
        if current.digest!=snapshot.footprint.digest or current.members!=snapshot.footprint.members:
            _fail('prose_context_changed','Protected acquired snapshot changed; ordinary FULL is required')
        return dict(sameContext=True,goFamilyArgv=[],observation=observation,
                    profileState='Unknown',qualityOutcomes=[],baselinePublished=False)
