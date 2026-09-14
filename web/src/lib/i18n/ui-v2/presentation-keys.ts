import { messages as labels } from "./copy/presentation-labels";
import { messages as messages } from "./copy/presentation-messages";
export const presentationKeys = Object.keys({...labels,...messages}).filter(key=>!key.includes("{")) as (keyof (typeof labels & typeof messages))[];
