// The desktop pet's window loads the app bundle; its label picks the pet.
import { getCurrentWindow } from "@tauri-apps/api/window";

import { PetToy } from "./PetToy";
import { TOY_LABELS, type ToyLabel } from "./toy-config";
import "./toys.css";

const LABELS = Object.values(TOY_LABELS) as string[];

export function toyWindowLabel(): ToyLabel | null {
  try {
    const label = getCurrentWindow().label;
    return LABELS.includes(label) ? (label as ToyLabel) : null;
  } catch {
    return null;
  }
}

export function ToyWindow({ label }: { label: ToyLabel }) {
  return label === TOY_LABELS.pet ? <PetToy /> : null;
}
