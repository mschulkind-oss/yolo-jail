#!/bin/python3
"""Source-scoped FULL metadata/flag read capture, never a quality baseline.
Only full capture may execute the selected Go's non-costly env whitelist. Prose
comparison is file/environment revalidation, including no Go metadata descendants.
"""
import sys
sys.dont_write_bytecode=True
from dataclasses import dataclass,field
from pathlib import Path
import hashlib,hmac,json,os,re,shlex,shutil,stat,struct,subprocess
from closure import Footprint,Limits,Unknown,Mutated,Unavailable,identity,protected,wrapper_reads

REPO_ROOT = Path(__file__).resolve().parents[2]

class Refused(Exception): pass

# Installed envcmd.runEnv's costly switch excludes every one of these fields.
# CGO_*FLAGS/PKG_CONFIG/GOGCCFLAGS would invoke compiler probes; do NOT query them.
FIELDS=('GOENV','GOROOT','GOTOOLDIR','GOVERSION','GOTOOLCHAIN','GOOS','GOARCH',
 'GOHOSTOS','GOHOSTARCH','GOMOD','GOWORK','GOCACHE','GOMODCACHE','GOPATH',
 'GOFLAGS','GOEXPERIMENT','CGO_ENABLED','CC','CXX','GOAMD64','GOTMPDIR','GOCACHEPROG',
 'GOPROXY','GOSUMDB','GOPRIVATE','GONOPROXY','GONOSUMDB','GOINSECURE','GOAUTH','GOFIPS140')
CGO_FLAGS=('CGO_CFLAGS','CGO_CPPFLAGS','CGO_CXXFLAGS','CGO_FFLAGS','CGO_LDFLAGS')
ENV_KEYS=tuple(dict.fromkeys((*FIELDS,*CGO_FLAGS,'HOME','PATH','XDG_CONFIG_HOME',
 'LD_LIBRARY_PATH','LD_PRELOAD','GCC_EXEC_PREFIX','COMPILER_PATH','LIBRARY_PATH',
 'CPATH','C_INCLUDE_PATH','CPLUS_INCLUDE_PATH','PKG_CONFIG','NPM_CONFIG_PREFIX',
 'YOLO_TEST_PI_PACKAGE','YOLO_TEST_PI_SUBAGENTS')))
SAFE_FIELDS=set(FIELDS)-{'GOFLAGS','CC','CXX','GOCACHEPROG','GOPROXY','GOSUMDB','GOPRIVATE','GONOPROXY','GONOSUMDB','GOINSECURE','GOAUTH'}


def bounded_text(path):
    with Path(path).open('rb') as f:data=f.read(1024*1024+1)
    if len(data)>1024*1024:raise Unknown('source/settings parser exceeds bounded metadata size')
    return data.decode()


def priority_candidates(name,env):
    selected=shutil.which(name,path=env.get('PATH',''));paths=[]
    for directory in env.get('PATH','').split(os.pathsep):
        candidate=Path(directory or '.')/name;paths.append(candidate.absolute())
        if selected and os.path.abspath(candidate)==os.path.abspath(selected):break
    return selected,paths


def scoped_reads(paths,trees=()):
    return identity(paths,trees=trees,limits=Limits())


@dataclass
class ReadDescriptor:
    state:str
    roots:list[str]
    footprint:Footprint
    limitation:str=''
    def report(self):
        return dict(state=self.state,roots=self.roots,identity=self.footprint.report(),limitation=self.limitation)


def scoped_flag_tree(paths,trees):
    """Named flag-root bytes with explicit contained/Nix-store alias edges.
    Directory links are inputs AND enqueue their declared target; no global
    filesystem walk, credential-home traversal or arbitrary external alias.
    """
    roots=[Path(p).absolute() for p in trees];queue=list(roots);seen=set();inputs=list(map(Path,paths));links={};edges=[]
    owned=Path(__file__).resolve().parent
    for root in roots:
        if not (root.is_relative_to('/nix/store') or root.is_relative_to(owned)):
            raise Unknown('Flag tree is outside this source-accounted Nix/owned fixture profile')
    while queue:
        path=Path(os.path.abspath(queue.pop()))
        if str(path) in seen:continue
        seen.add(str(path))
        if len(seen)+len(inputs)>Limits().entries:raise Unknown('Declared read membership exceeds bounds')
        try:s=path.lstat()
        except FileNotFoundError:inputs.append(path);continue
        if stat.S_ISLNK(s.st_mode):
            text=os.readlink(path);target=Path(text);target=target if target.is_absolute() else path.parent/target
            target=Path(os.path.abspath(target))
            try:canonical=target.resolve(strict=True)
            except (OSError,RuntimeError):raise Unknown('Unsupported missing/cyclic declared tree alias')
            if not (any(canonical.is_relative_to(r.resolve()) for r in roots) or canonical.is_relative_to('/nix/store')):
                raise Unknown('Declared alias leaves accounted root/Nix read graph')
            links[str(path)]='link:'+str((s.st_dev,s.st_ino,s.st_mode))+':'+text
            edges.append(dict(binding=str(path),target=str(target)));queue.append(target)
        elif stat.S_ISDIR(s.st_mode):
            inputs.append(path)
            with os.scandir(path) as entries:queue.extend(Path(e.path) for e in entries)
        elif stat.S_ISREG(s.st_mode):inputs.append(path)
        else:raise Unknown('Unsupported declared read type')
    fp=scoped_reads(inputs)
    fp.members.update(links)
    if len(fp.members)>Limits().entries:raise Unknown('Merged alias bindings exceed bounds')
    fp.digest=hashlib.sha256(json.dumps(sorted(fp.members.items()),separators=(',',':')).encode()).hexdigest()
    return fp,edges


def go_quoted_split(value):
    """Go cmd/internal/quoted.Split: field-start quotes; no unescaping.
    Only space, tab, LF and CR delimit fields. Unsupported encodings are Unknown.
    """
    if not isinstance(value,str):raise Unknown('Unsupported Go flag string type')
    try:value.encode('utf-8')
    except UnicodeError:raise Unknown('Unsupported Go flag encoding') from None
    fields=[];index=0
    while index<len(value):
        while index<len(value) and value[index] in ' \t\n\r':index+=1
        if index==len(value):break
        if value[index] in '\"\'':
            end=value.find(value[index],index+1)
            if end<0:raise Unknown('Unterminated Go quoted field')
            fields.append(value[index+1:end]);index=end+1
        else:
            end=index
            while end<len(value) and value[end] not in ' \t\n\r':end+=1
            fields.append(value[index:end]);index=end
    return fields


def bash_support_tokens(value):
    """Keep the existing literal wrapper-support tokenizer separate from Go."""
    return shlex.split(value)


def flag_descriptor(flag_files,extra_flags):
    """Literal include/library/prefix read roots; not a general GCC option parser."""
    files=[Path(p) for p in flag_files];roots=set();unknown=[]
    strings=[(bounded_text(p),bash_support_tokens) for p in files if p.is_file()]+[(value,go_quoted_split) for value in extra_flags]
    for body,token_reader in strings:
        if any(x in body for x in ('@','`','$','*','?')):
            unknown.append('opaque response/expansion reader');continue
        try:tokens=token_reader(body)
        except (Unknown,ValueError):unknown.append('opaque flag quoting');continue
        i=0
        while i<len(tokens):
            token=tokens[i];value=None
            if token in ('-I','-L','-B','-idirafter','-isystem','-iquote','-include','-imacros','-isysroot','--sysroot'):
                i+=1
                if i>=len(tokens):unknown.append('flag missing read path');break
                value=tokens[i]
            elif token.startswith(('-I','-L','-B')) and len(token)>2:value=token[2:]
            elif token.startswith('--sysroot='):value=token.split('=',1)[1]
            elif token.startswith('-Wl,'):
                unknown.append('linker argument parser outside this literal flag scope')
            if value is not None:
                if not value:unknown.append('empty read path needs actual invocation working directory')
                elif not Path(value).is_absolute():unknown.append('relative flag root needs actual invocation working directory')
                else:roots.add(str(Path(value)))
            i+=1
    paths=list(files);trees=[]
    for text in sorted(roots):
        p=Path(text)
        (paths if p.is_file() else trees).append(p)
    try:fp,aliases=scoped_flag_tree(paths,trees)
    except (Unknown,OSError,ValueError):
        return ReadDescriptor('Unknown',sorted(roots),scoped_reads(files),'contained read tree requires an additional accounted alias/root or exceeded bounds')
    fp.digest=hashlib.sha256((fp.digest+json.dumps(extra_flags,separators=(',',':'))).encode()).hexdigest()
    result=ReadDescriptor('Unknown' if unknown else 'Known',sorted(roots),fp,'; '.join(sorted(set(unknown))))
    result.aliases=aliases
    return result


@dataclass(frozen=True)
class OfficialResult:
    """Hook payload from fetchToolchain download + allowExec + version commands.
    A validated payload is NOT execution provenance or a quality outcome.
    """
    module:str
    directory:str
    checksum:str
    download_exit:int
    ready:bool
    version_output:str
    version_exit:int


class OfficialHook:
    """Capture normal full command results without downloading/provisioning here."""
    def __init__(self):self.download_result=None;self.ready=None;self.version_result=None
    def guard(self,route):
        if route!='full':raise Refused('Official command-result hook is full-only')
    def download(self,route,module,output,exit_code):
        self.guard(route)
        try:info=json.loads(output)
        except (ValueError,TypeError):raise Unknown('Invalid actual download JSON')
        if not isinstance(info,dict) or info.get('Error') or not isinstance(info.get('Dir'),str) or not isinstance(info.get('Sum'),str):
            raise Unknown('Actual download returned no usable Dir/Sum')
        self.download_result=(module,info['Dir'],info['Sum'],exit_code)
        self.ready=None;self.version_result=None
    def readiness(self,route,directory,success):
        self.guard(route)
        if self.download_result is None or directory!=self.download_result[1] or type(success) is not bool:raise Unknown('Readiness must bind the actual chosen download Dir')
        self.ready=success
    def version(self,route,binary,output,exit_code):
        self.guard(route)
        if self.download_result is None or Path(binary)!=Path(self.download_result[1])/'bin/go':raise Unknown('Version must name actual chosen download binary')
        self.version_result=(output,exit_code)
    def result(self):
        if self.download_result is None or self.ready is None or self.version_result is None:return None
        return OfficialResult(*self.download_result,self.ready,*self.version_result)


def official_descriptor(result,pin,goos,goarch):
    if result is None:return 'Unknown',None,'Normal full must supply actual download Dir/Sum/readiness/version result; provisioning remains allowed'
    if not isinstance(result,OfficialResult):raise Refused('official hook requires its typed command result')
    expected='golang.org/toolchain@v0.0.1-'+pin+'.'+goos+'-'+goarch
    if (result.module!=expected or type(result.download_exit) is not int or result.download_exit!=0 or
        type(result.version_exit) is not int or result.version_exit!=0 or result.ready is not True or
        not isinstance(result.checksum,str) or not re.fullmatch(r'h1:[A-Za-z0-9+/]{43}=',result.checksum) or
        not isinstance(result.version_output,str) or result.version_output.strip()!='go version '+pin+' '+goos+'/'+goarch or
        not isinstance(result.directory,str) or not Path(result.directory).is_absolute()):
        return 'Unknown',None,'Incomplete/nonmatching actual download/version/readiness result'
    root=Path(result.directory);binary=root/'bin/go'
    if not binary.is_file() or not os.access(binary,os.X_OK):return 'Unknown',None,'Chosen result binary is absent/not executable; normal full may prepare it'
    try:fp=scoped_reads((binary,root/'VERSION'))
    except (Unknown,OSError):return 'Unknown',None,'Chosen result binding is unavailable'
    return 'Known',fp,'Typed chosen-result binding only; distribution transitive bytes and quality outcomes are separate'


def elf_descriptor(path):
    """Bounded ELF64 little-endian PT_INTERP/DT_NEEDED/RPATH/RUNPATH extraction.
    Does not execute ldd/the binary or pretend to emulate the loader.
    """
    path=Path(path)
    try:
        with path.open('rb') as f:
            size=path.stat().st_size;charged=0
            def read(offset,n):
                nonlocal charged
                if offset<0 or n<0 or offset+n>size or charged+n>16*1024*1024:raise Unknown('ELF metadata exceeds bounded supported layout')
                f.seek(offset);data=f.read(n);charged+=len(data)
                if len(data)!=n:raise Unknown('ELF metadata truncated')
                return data
            hdr=read(0,64)
            if hdr[:6]!=b'\x7fELF\x02\x01':raise Unknown('unsupported helper ELF format')
            values=struct.unpack('<16sHHIQQQIHHHHHH',hdr);phoff=values[5];phsize=values[9];phnum=values[10]
            if phsize!=56 or phnum>1024:raise Unknown('unsupported ELF program table')
            segments=[struct.unpack('<IIQQQQQQ',read(phoff+i*phsize,phsize)) for i in range(phnum)]
            interpreter=None;dynamic=[]
            for typ,flags,offset,vaddr,paddr,filesz,memsz,align in segments:
                if typ==3:interpreter=read(offset,filesz).rstrip(b'\0').decode()
                if typ==2:
                    if filesz%16:raise Unknown('ELF dynamic table alignment')
                    for at in range(offset,offset+filesz,16):
                        tag,val=struct.unpack('<qQ',read(at,16))
                        if tag==0:break
                        dynamic.append((tag,val))
            string_address=next((v for t,v in dynamic if t==5),None)
            string_size=next((v for t,v in dynamic if t==10),0)
            strings=b''
            if string_address is not None:
                mapped=next((p for p in segments if p[0]==1 and p[3]<=string_address and string_address+string_size<=p[3]+p[5]),None)
                if mapped is None:raise Unknown('unmapped ELF string table')
                strings=read(mapped[2]+string_address-mapped[3],string_size)
            def text(index):
                if index>=len(strings) or b'\0' not in strings[index:]:raise Unknown('invalid ELF dynamic string')
                return strings[index:].split(b'\0',1)[0].decode()
            return dict(state='Known',scope='ELF-declared metadata only, not dynamic-loader read closure',
                interpreter=interpreter,needed=[text(v) for t,v in dynamic if t==1],
                rpath=[text(v) for t,v in dynamic if t==15],runpath=[text(v) for t,v in dynamic if t==29],
                bytesRead=charged)
    except (Unknown,OSError,ValueError,UnicodeError,struct.error) as exc:
        return dict(state='Unknown',limitation=type(exc).__name__+': unsupported/unavailable ELF layout')


def helper_descriptor(path,env,loader_config=('/etc/ld.so.cache','/etc/ld.so.preload')):
    """Bind declared interpreter and candidates without inventing loader semantics."""
    path=Path(path);elf=elf_descriptor(path);paths=[path,*map(Path,loader_config)];search=[]
    if elf['state']=='Known':
        if elf['interpreter']:paths.append(Path(elf['interpreter']))
        search.extend(env.get('LD_LIBRARY_PATH','').split(':') if env.get('LD_LIBRARY_PATH') else [])
        for value in elf['runpath'] or elf['rpath']:
            search.extend(value.replace('${ORIGIN}',str(path.parent)).replace('$ORIGIN',str(path.parent)).split(':'))
        for name in elf['needed']:
            for directory in search:
                if directory and Path(directory).is_absolute() and '/' not in name:paths.append(Path(directory)/name)
    try:fp=scoped_reads(paths)
    except (Unknown,OSError):fp=scoped_reads((path,));elf=dict(state='Unknown',limitation='additional interpreter/library alias root required')
    closed=elf['state']=='Known' and not elf['needed'] and not env.get('LD_PRELOAD')
    return ReadDescriptor('Known' if closed else 'Unknown',sorted(set(search)),fp,
        '' if closed else 'Exact missing reader: dynamic loader DT_NEEDED search, ld.so.cache/preload/hwcaps and external runtime resolution are not source-closed'),elf


def read_go_settings(path):
    """Go cfg.readEnvFile's LF-only/untrimmed first-byte semantics (UTF-8 scope)."""
    values={}
    p=Path(path)
    if not p.is_file():return values
    if p.stat().st_size>1024*1024:raise Unknown('Go settings exceed bounded metadata size')
    try:data=bounded_text(p)
    except UnicodeError:raise Unknown('Unsupported Go settings encoding') from None
    for line in data.split('\n'):
        key,sep,value=line.partition('=')
        if sep and 'A'<=line[0]<='Z':values[key]=value
    return values


@dataclass
class Snapshot:
    metadata_state:str
    profile_state:str
    official_state:str
    footprint:Footprint
    raw_metadata:dict=field(repr=False)
    report_data:dict
    quality_outcomes:list=field(default_factory=list)
    def report(self):return self.report_data


class FullCapture:
    def __init__(self,env,cwd,runner=None):
        self.env=dict(env);self.cwd=Path(cwd).resolve();self.runner=runner or self._live_metadata
        self.key=os.urandom(32);self.commands=[];self.last={}
        selected=shutil.which('go',path=self.env.get('PATH',''))
        if not selected:raise Unknown('No selected Go; ordinary full may provision it')
        self.lexical_go=Path(selected).absolute();self.go=self.lexical_go.resolve(strict=True)
        self.pin_source=REPO_ROOT/'tools'/'pack-binaries'/'toolchain.go'
        self.pin=re.search(r'const Toolchain = "(go[0-9.]+)"',self.pin_source.read_text()).group(1)

    def opaque(self,value):return hmac.new(self.key,str(value).encode(),hashlib.sha256).hexdigest()
    def safe_environment(self,env):
        keys=list(ENV_KEYS)+[k for k in env if k.startswith(('NIX_CFLAGS','NIX_LDFLAGS','NIX_CC_WRAPPER','NIX_BINTOOLS_WRAPPER','NIX_DYNAMIC','NIX_HARDENING'))]
        return {k:dict(present=k in env,identity=self.opaque(env.get(k,''))) for k in sorted(set(keys))}
    def safe_metadata(self,values):
        return {k:(v if k in SAFE_FIELDS else dict(identity=self.opaque(v),present=bool(v))) for k,v in values.items()}

    def _live_metadata(self,argv,*,cwd,env):
        # stdout is parsed in memory. Raw env/stderr are never persisted/printed.
        result=subprocess.run(argv,cwd=cwd,env=env,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=30,check=False)
        if result.returncode:raise Unknown('Selected Go metadata failed; retry ordinary full under existing policy')
        if len(result.stdout)>1024*1024:raise Unknown('Go metadata exceeds bounded response size')
        try:return json.loads(result.stdout)
        except (ValueError,UnicodeError):raise Unknown('Selected Go returned invalid metadata')

    def metadata(self,route,target=None):
        if route!='full':raise Refused('Go metadata is allowed only during FULL capture, never prose')
        env=dict(self.env)
        if target:env['GOOS']=target  # Justfile's explicit normal lint overrides only.
        selected,self.go_candidates=priority_candidates('go',env)
        if not selected:raise Unknown('Selected Go disappeared; retry normal full')
        self.lexical_go=Path(selected).absolute();self.go=self.lexical_go.resolve(strict=True)
        argv=[str(self.go),'env','-json',*FIELDS]
        self.commands.append(dict(route='full',argv=argv,recipeGOOS=target,inheritedEnvironmentUnmodifiedExceptRecipeGOOS=True))
        values=self.runner(argv,cwd=self.cwd,env=env)
        if not isinstance(values,dict) or set(values)!=set(FIELDS) or any(not isinstance(v,str) for v in values.values()):
            raise Unknown('Missing/non-string effective metadata whitelist; no default context guessed')
        for k in ('GOROOT','GOTOOLDIR','GOCACHE','GOMODCACHE'):
            if not Path(values[k]).is_absolute():raise Unknown('Missing/nonabsolute effective '+k)
        if values['GOENV'] not in ('','off') and not Path(values['GOENV']).is_absolute():raise Unknown('Opaque GOENV binding')
        return values

    def settings(self,values,env):
        defaults=read_go_settings(Path(values['GOROOT'])/'go.env')
        user=read_go_settings(values['GOENV']) if values['GOENV'] not in ('','off') else {}
        defaults.update(user)
        for k in CGO_FLAGS:
            defaults[k]=env.get(k) or defaults.get(k) or ('' if k=='CGO_CPPFLAGS' else '-O2 -g')
        return {k:defaults[k] for k in CGO_FLAGS}

    def config_paths(self,values):
        paths=[*self.go_candidates,self.lexical_go,self.go,self.pin_source,self.cwd/'Justfile',
            self.pin_source.with_name('recipe.go'),
            Path(values['GOROOT'])/'bin/go',Path(values['GOROOT'])/'VERSION',Path(values['GOROOT'])/'go.env',
            Path(values['GOROOT'])/'src',Path(values['GOTOOLDIR'])]
        if values['GOENV'] not in ('','off'):paths.append(Path(values['GOENV']))
        mod=values['GOMOD']
        if mod not in ('','/dev/null'):
            p=Path(mod);paths.extend((p,p.with_name('go.sum'),p.parent/'vendor/modules.txt'))
        else:raise Unknown('No selected repository module context')
        work=values['GOWORK']
        if work not in ('','off'):
            p=Path(work);paths.extend((p,p.with_name(p.name+'.sum')))
            # Bind workspace member module selectors. More complex replacements
            # need the normal full loader result; do not run go list on prose.
            body=bounded_text(p)
            block=re.search(r'(?ms)^use\s*\((.*?)\)',body)
            lines=block.group(1).splitlines() if block else []
            lines+=re.findall(r'(?m)^use\s+([^\n(]+)',body)
            for line in lines:
                line=line.split('//',1)[0].strip()
                if not line:continue
                tokens=shlex.split(line)
                if len(tokens)!=1:raise Unknown('Opaque workspace member syntax')
                member=Path(tokens[0]);member=member if member.is_absolute() else p.parent/member
                paths.extend((member/'go.mod',member/'go.sum'))
        return paths

    def compiler(self,values,env):
        command=shlex.split(values['CC'])
        if len(command)!=1:return None,dict(state='Unknown',limitation='Opaque compiler launcher/arguments')
        selected,cc_candidates=priority_candidates(command[0],env)
        if not selected:return None,dict(state='Unknown',limitation='Selected compiler is absent; normal full may provide it')
        try:component,graph=wrapper_reads(selected)
        except (Unknown,OSError,UnicodeError):return None,dict(state='Unknown',limitation='Compiler wrapper source is outside audited literal profile')
        files=[Path(p) for p in component.footprint.members if '/nix-support/' in p and Path(p).is_file() and Path(p).name not in ('utils.bash','add-flags.sh','add-hardening.sh','darwin-sdk-setup.bash')]
        flag_files=[p for p in files if p.name.endswith(('cflags','ldflags','cflags-before','ldflags-before','cxxflags'))]
        flag_reads=flag_descriptor(flag_files,list(self.settings(values,env).values()))
        literal=[Path(p) for p in graph['furtherToolAndIncludeRoots'] if Path(p).is_file()]
        required=scoped_reads([*cc_candidates,selected,*literal,*map(Path,component.footprint.members)])
        merged=dict(required.members);merged.update(flag_reads.footprint.members)
        fp=merge_footprints((required,flag_reads.footprint))
        opaque=[k for k,v in env.items() if v and (k.startswith(('NIX_CFLAGS','NIX_LDFLAGS','NIX_CC_WRAPPER','NIX_BINTOOLS_WRAPPER','NIX_DYNAMIC','NIX_HARDENING')) or k in ('GCC_EXEC_PREFIX','COMPILER_PATH','LIBRARY_PATH','CPATH','C_INCLUDE_PATH','CPLUS_INCLUDE_PATH','LD_PRELOAD'))]
        helpers=[]
        for p in literal:
            if p.name=='expand-response-params':
                helper,elf=helper_descriptor(p,env);helpers.append(dict(path=str(p),descriptor=helper.report(),elf=elf))
                fp=merge_footprints((fp,helper.footprint))
        report=dict(state='Unknown',scope='literal wrapper, flag-derived contained include/library bytes and selected helper bindings',
            flagReadScope=flag_reads.report(),declaredAliasEdges=getattr(flag_reads,'aliases',[]),supportReadScope=component.report(),
            literalExecutables=[str(p) for p in literal],helpers=helpers,opaqueEnvironmentKeys=opaque,
            completeCompilerClosure=False,
            exactMissingReader='response-helper dynamic loader DT_NEEDED/search plus GCC invocation/package-specific/probe reads remain outside this scope')
        return fp,report

    def discover(self,metadata,env,official):
        parts=[];compiler_reports={};compiler_cache={}
        for name,values in metadata.items():
            parts.append(scoped_reads(self.config_paths(values)))
            # Identical named read scopes across lint targets are read once per
            # discovery, not once per target or unrelated unit control.
            selector=(values['CC'],values['GOROOT'],values['GOENV'])
            if selector not in compiler_cache:compiler_cache[selector]=self.compiler(values,env)
            fp,report=compiler_cache[selector]
            if fp:parts.append(fp)
            compiler_reports[name]=report
        base=metadata['base']
        state,official_fp,why=official_descriptor(official,self.pin,base['GOHOSTOS'],base['GOHOSTARCH'])
        if official_fp:parts.append(official_fp)
        fp=merge_footprints(parts)
        context=self.opaque(json.dumps({'metadata':metadata,'environment':self.safe_environment(env),'official':official.__dict__ if official else None},sort_keys=True))
        fp.digest=self.opaque(fp.digest+context)
        self.last=dict(compiler=compiler_reports,officialState=state,officialReason=why)
        return fp

    def capture_full(self,route='full',official=None,verify=lambda:None):
        if route!='full':raise Refused('Full capture cannot run on prose')
        metadata={}
        def discover():
            metadata.clear();metadata.update({name:self.metadata('full',target) for name,target in (('base',None),('lint-linux','linux'),('lint-darwin','darwin'))})
            return self.discover(metadata,self.env,official)
        fp,observation=protected(discover,verify=verify)
        report=dict(scope='automatic FULL metadata and named read descriptors; NOT quality/provenance',
            metadataState='Known',profileState='Unknown',metadata={k:self.safe_metadata(v) for k,v in metadata.items()},
            environment=self.safe_environment(self.env),selectedGo=dict(lexical=str(self.lexical_go),resolved=str(self.go)),
            sourcePin=self.pin,officialState=self.last['officialState'],officialReason=self.last['officialReason'],
            officialResult=official.__dict__ if official else None,compiler=self.safe_descriptors(self.last['compiler']),identity=self.safe_footprint(fp),
            observation=observation,commandCount=len(self.commands),qualityOutcomes=[],baselinePublished=False,
            contextLimitations=['Go installation byte closure separate from metadata bindings','package-specific/compiler-probe flags require normal full results','Node/Pi external ancestor/runtime readers not claimed'])
        snapshot=Snapshot('Known','Unknown',self.last['officialState'],fp,dict(metadata),report)
        snapshot.official=official
        return snapshot

    def safe_descriptors(self,value):
        if isinstance(value,dict):return {k:(self.opaque(v) if k=='digest' else self.safe_descriptors(v)) for k,v in value.items()}
        if isinstance(value,list):return [self.safe_descriptors(v) for v in value]
        return value

    def safe_footprint(self,fp):
        report=fp.report();report['digest']=self.opaque(fp.digest);report['digestKind']='session-keyed identity; not transferable provenance'
        return report

    def compare_prose(self,snapshot,env,verify=lambda:None):
        count=len(self.commands)
        final,observation=protected(lambda:self.discover(snapshot.raw_metadata,dict(env),snapshot.official),verify=verify)
        assert len(self.commands)==count
        return dict(sameContext=final.digest==snapshot.footprint.digest,goFamilyArgv=[],observation=observation,
            qualityOutcomes=[],baselinePublished=False,profileState='Unknown')


def merge_footprints(parts):
    members={}
    for part in parts:members.update(part.members)
    files=[v for v in members.values() if v.startswith('file:')]
    import ast
    total=sum(ast.literal_eval(v.removeprefix('file:').rsplit(':',1)[0])[3] for v in files)
    if len(members)>Limits().entries or total>Limits().bytes:raise Unknown('Merged metadata/read descriptor exceeds bounds')
    digest=hashlib.sha256(json.dumps(sorted(members.items()),separators=(',',':')).encode()).hexdigest()
    return Footprint(members,digest,len(files),sum(v.startswith('dir:') for v in members.values()),total)
