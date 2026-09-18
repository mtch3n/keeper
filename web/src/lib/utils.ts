import { createCn } from "cn/config"

/**
 * Class merging that knows the type scale. Without this, the merger reads
 * `text-meta` or `text-title` as a colour, so `cn("text-meta", "text-danger")`
 * silently drops the size. Every size role in index.css is listed here.
 */
export const cn = createCn({
  extend: {
    classGroups: {
      "font-size": [{ text: ["title", "heading", "body", "label", "meta"] }],
    },
  },
})
