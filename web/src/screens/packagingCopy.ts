/** What went wrong and what to do about it, keyed by the product-master problem
 *  code (the last segment of problem.type). The service's own title/detail is
 *  still shown beneath it. */
export const PACK_PROBLEM_LEADS: Record<string, string> = {
  "invalid-gtin": "This GTIN is not valid. Check the digits and the check digit against the label or the GS1 record, then try again.",
  "variable-measure-unsupported": "Variable-measure GTINs (starting with 9) are not supported. Use the fixed-measure GTIN of the pack.",
  "invalid-pack-level": "That is not a pack level. Choose EACH, INNER, CASE or PALLET.",
  "invalid-pack-quantity": "The quantity is not valid. A level above EACH holds 2 to 1,000,000 of the level below it, and the eaches per pack must agree with the chain.",
  "invalid-dimension": "A dimension is out of range. Lengths are 1 to 20,000 mm and weight is 1 to 2,000,000 g.",
  "invalid-reason": "The retire reason must be 1 to 200 characters without control characters.",
  "pack-quantity-immutable":
    "An active level cannot change how many it holds. Declare the new quantity with a NEW GTIN at this level: the old one is end-dated and stays resolvable.",
  "gtin-already-assigned":
    "This GTIN already identifies another pack level (or a retired one) and is never reused. Use the GTIN printed on this pack, or look it up to see where it is assigned.",
  "pack-level-order":
    "A level needs an ACTIVE lower level to contain. Declare the child level first (EACH, then INNER, CASE, PALLET) and give its GTIN as the child GTIN.",
  "pack-dimensions-below-contents":
    "The pack weighs less than the packs it contains. Check the weight, or the quantity of the child level.",
  "concurrent-modification": "Someone else changed this product at the same moment. Nothing was saved: reload and try again.",
  "product-not-found": "No product is registered under this SKU. Register it first.",
  "pack-level-not-found": "This product never declared that level, so there is nothing to retire.",
  "gtin-not-found": "No pack level holds this GTIN. Check the digits, or declare the GTIN on a product's pack level.",
};
