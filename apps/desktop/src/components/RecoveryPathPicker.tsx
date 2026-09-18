import { useState } from "react";
import { useRecoveryPaths } from "../use-recovery-paths";
import { RecoveryPathEditor } from "../RecoveryPathEditor";
import { pathNodes } from "../recovery-path-model";
import type { RoutableService } from "../service-model";
import { useT } from "../i18n";
import { FilterSelect } from "./FilterSelect";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "./ui/dialog";
import { Panel } from "./Panel";
import { ConfirmDialog } from "./ConfirmDialog";
export function RecoveryPathPicker({value,onChange,protocol,services,ready,auto=false,hasOverride=false,onSaveLegacy}:{value?:string;onChange:(id:string|undefined)=>void;protocol:string;services:RoutableService[];ready:boolean;auto?:boolean;hasOverride?:boolean;onSaveLegacy?:()=>void}){
 const t=useT(),catalog=useRecoveryPaths(ready),[open,setOpen]=useState(false),[dirty,setDirty]=useState(false),[discard,setDiscard]=useState(false);
 const selected=catalog.records.find(record=>record.path.id===value);
 const available=catalog.records.filter(record=>record.path.protocol===protocol&&(!auto||pathNodes(record.path).every(node=>node.upstream_model)));
 const options=[{value:"",label:t("paths.inline")},...available.map(record=>({value:record.path.id,label:record.path.name}))];if(value&&!available.some(record=>record.path.id===value))options.push({value,label:selected?.path.name??value});
 return <Panel className="grid gap-2 p-3"><FilterSelect label={t("paths.use")} ariaLabel={t("paths.use")} value={value??""} onChange={value=>onChange(value||undefined)} options={options}/>{selected?<p className="break-words text-xs text-muted-foreground">{pathNodes(selected.path).map(node=>`${services.find(service=>service.id===node.service_id)?.name??node.service_id}${node.upstream_model?` / ${node.upstream_model}`:""}`).join(" → ")}</p>:null}{value&&hasOverride?<p className="text-xs text-muted-foreground">{t("paths.hasOverride")}</p>:null}<div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={!ready} onClick={()=>setOpen(true)}>{t("paths.new")}</Button>{!value&&onSaveLegacy?<Button type="button" variant="ghost" onClick={onSaveLegacy}>{t("paths.saveLegacy")}</Button>:null}{catalog.error?<Button type="button" variant="ghost" onClick={catalog.reload}>{t("common.retry")}</Button>:null}</div><Dialog open={open} onOpenChange={next=>{if(!next&&dirty)setDiscard(true);else setOpen(next)}}><DialogContent className="flex h-[85vh] max-w-4xl flex-col overflow-hidden p-0"><DialogHeader className="shrink-0 border-b p-4"><DialogTitle>{t("paths.new")}</DialogTitle></DialogHeader><RecoveryPathEditor services={services} ready={ready} initialProtocol={protocol} onDirtyChange={setDirty} onSaved={record=>{setOpen(false);setDirty(false);onChange(record.path.id);catalog.reload()}}/></DialogContent></Dialog><ConfirmDialog open={discard} title={t("paths.discard")} description={t("paths.discardHint")} confirmLabel={t("common.confirm")} onCancel={()=>setDiscard(false)} onConfirm={()=>{setDiscard(false);setOpen(false);setDirty(false)}}/></Panel>
}
