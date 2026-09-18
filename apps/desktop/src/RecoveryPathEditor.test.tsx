// @vitest-environment happy-dom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { beforeEach, afterEach, describe, it, expect, vi } from "vitest";
const bridge=vi.hoisted(()=>({getRoutingSettings:vi.fn(),previewRecoveryPath:vi.fn(),createRecoveryPath:vi.fn(),updateRecoveryPath:vi.fn()}));
vi.mock("./bridge",()=>bridge);
import { RecoveryPathEditor } from "./RecoveryPathEditor";
import { parseRecoveryPath, parseRecoveryPreview, type RecoveryPathRecord } from "./recovery-path-model";
import { defaultFailurePolicy } from "./failure-policy-model";
import { applyLocale } from "./i18n";
import type { RoutableService } from "./service-model";
const services:RoutableService[]=["a","b","c"].map(id=>({id:`service_${id}`,name:id.toUpperCase(),enabled:true,models:["model"],capabilities:[{protocol:"openai.responses",mode:"native",streaming:true}]}));
const record=():RecoveryPathRecord=>({path:{id:"path_test",name:"Shared",protocol:"openai.responses",mode:"automatic",targets:["a","b","c"].map(id=>({id:`node_${id}`,service_id:`service_${id}`,upstream_model:"model",upstream_protocol:"openai.responses",plan_type:"native"}))},etag:`"sha256:${"a".repeat(64)}"`,references:[{name:"Code alias",route_id:"route_code",protocol:"openai.responses",override:false}]});
describe("reusable path editor",()=>{
 let container:HTMLDivElement,root:Root;
 beforeEach(async()=>{await applyLocale("zh-CN");(globalThis as typeof globalThis&{IS_REACT_ACT_ENVIRONMENT:boolean}).IS_REACT_ACT_ENVIRONMENT=true;vi.clearAllMocks();bridge.getRoutingSettings.mockResolvedValue({default_failure_policy:defaultFailurePolicy(),allow_unmatched_failover:false,strategy:"retry_first",max_attempts:6});bridge.previewRecoveryPath.mockImplementation(async input=>({steps:input.path.targets.flatMap((node:Record<string,string>)=>[0,1].map(index=>({step_id:node.id,service_id:node.service_id,model:"model",action:index?"retry":"initial",status:"failed",reason:"network_error",wait_min_ms:0,wait_max_ms:0}))),stop_reason:"attempt_limit",max_attempts:6}));bridge.updateRecoveryPath.mockImplementation(async(_id,_etag,input)=>({...record(),path:{...input,id:"path_test"}}));container=document.createElement("div");document.body.append(container);root=createRoot(container)});
 afterEach(async()=>{await act(async()=>root.unmount());container.remove()});
 async function render(){await act(async()=>root.render(<RecoveryPathEditor record={record()} services={services} ready onSaved={()=>{}}/>));await act(async()=>{await new Promise(resolve=>setTimeout(resolve,250))})}
 const button=(label:string)=>[...document.querySelectorAll<HTMLButtonElement>("button")].find(button=>button.textContent===label)!;
 it("orders providers with buttons, previews through Core, and saves array order",async()=>{
  await render();expect(container.textContent).toContain("Code alias");expect(bridge.previewRecoveryPath).toHaveBeenCalledWith(expect.objectContaining({error:"network_error"}));
  const down=container.querySelector<HTMLButtonElement>('button[aria-label="下移"]')!;await act(async()=>down.click());
  await act(async()=>button("保存调用路径").click());
  expect(bridge.updateRecoveryPath).toHaveBeenCalledWith("path_test",record().etag,expect.objectContaining({targets:[expect.objectContaining({service_id:"service_b"}),expect.objectContaining({service_id:"service_a"}),expect.objectContaining({service_id:"service_c"})]}));
 });
 it("confirms conversion and retains repeated calls as distinct steps",async()=>{
  await render();await act(async()=>button("转为逐步编辑").click());expect(document.querySelector('[role="alertdialog"]')).not.toBeNull();await act(async()=>button("使用这个顺序").click());
  await act(async()=>button("保存调用路径").click());const input=bridge.updateRecoveryPath.mock.calls[0][2];expect(input.mode).toBe("steps");expect(input.targets).toBeUndefined();expect(input.steps.map((node:Record<string,string>)=>node.service_id)).toEqual(["service_a","service_a","service_b","service_b","service_c","service_c"]);expect(new Set(input.steps.map((node:Record<string,string>)=>node.id)).size).toBe(6);
 });
 it("keeps an edited draft after saving fails",async()=>{
  await render();bridge.updateRecoveryPath.mockRejectedValueOnce(Error("version changed"));await act(async()=>container.querySelector<HTMLButtonElement>('button[aria-label="下移"]')!.click());await act(async()=>button("保存调用路径").click());expect(container.textContent).toContain("version changed");expect(button("保存调用路径").disabled).toBe(false);
 });
});
describe("path IPC validation",()=>{
 it("distinguishes repeating steps from duplicate automatic targets",()=>{const path=record().path;expect(()=>parseRecoveryPath({...path,targets:[path.targets![0],{...path.targets![0],id:"node_other"}]})).toThrow();const {targets:_,...rest}=path;expect(parseRecoveryPath({...rest,mode:"steps",steps:[path.targets![0],{...path.targets![0],id:"node_other"}]}).steps).toHaveLength(2)});
 it("rejects invalid counts, unknown properties, and preview states",()=>{const path=record().path;expect(()=>parseRecoveryPath({...path,max_attempts:21})).toThrow();expect(()=>parseRecoveryPath({...path,other:true})).toThrow();expect(()=>parseRecoveryPreview({steps:[{step_id:"node_a",service_id:"service_a",model:"model",action:"send",status:"failed",wait_min_ms:0,wait_max_ms:0}],stop_reason:"error_rule",max_attempts:6})).toThrow()});
});
