import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ProductDetailRoute } from "./ProductDetailScreen";
import { json, mockApi, pending, problem } from "../test/fetchMock";
import type { Handler } from "../test/fetchMock";
import { BARE, MEASURED_PROFILE, product } from "../test/fixtures";

function mount(sku = "SKU-1") {
  return render(
    <MemoryRouter initialEntries={[`/products/${sku}`]}>
      <Routes>
        <Route path="/products/:sku" element={<ProductDetailRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

function routes(over: Record<string, Handler> = {}) {
  return mockApi({ "GET /products/SKU-1": json(product()), ...over });
}

/** The ui-kit Card (a <section>) whose header reads `title`. */
function card(title: string): HTMLElement {
  const section = screen.getByRole("heading", { name: title }).closest("section");
  if (!section) throw new Error(`no card titled ${title}`);
  return section;
}

function form(name: string) {
  return within(screen.getByRole("form", { name }));
}

describe("ProductDetailScreen read side", () => {
  it("shows a loading state", () => {
    routes({ "GET /products/SKU-1": pending() });
    mount();
    expect(screen.getByText("Loading product…")).toBeInTheDocument();
  });

  it("shows the version, the classification and the physical profile with the discrepancy flag", async () => {
    routes();
    mount();
    expect(await screen.findByRole("heading", { name: "SKU-1" })).toBeInTheDocument();
    expect(screen.getByText(/Lithium battery pack 12V · version/)).toHaveTextContent("version 4");

    const classification = within(card("Classification"));
    expect(classification.getByText("Hazmat")).toHaveAttribute("data-tone", "danger");
    expect(classification.getByText("TemperatureSensitive")).toBeInTheDocument();
    expect(classification.getByText("Frozen")).toBeInTheDocument();
    expect(classification.getByText("3")).toBeInTheDocument(); // DOT hazard class
    expect(classification.getByText("native")).toBeInTheDocument();

    const profile = card("Physical profile");
    expect(profile.querySelector('[data-status="Discrepancy"]')).toHaveAttribute("data-tone", "warning");
    expect(within(profile).getByText(/differ by more than 10 %/)).toBeInTheDocument();

    const rows = within(screen.getByRole("table")).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("Declared200 × 120 × 80 mm1,920,000 mm³1,500 g"),
      expect.stringContaining("Measured205 × 121 × 82 mm2,034,010 mm³1,720 g2026-10-06 14:05ZCUBISCAN-03"),
      expect.stringContaining("Effective (measured)205 × 121 × 82 mm"),
    ]);
  });

  it("renders a registered-only product as not classified and with nothing known physically", async () => {
    mockApi({ "GET /products/SKU-2": json(BARE) });
    mount("SKU-2");
    expect(await screen.findByText("Not classified yet.")).toBeInTheDocument();
    expect(screen.getByText("(no description)", { exact: false })).toBeInTheDocument();
    expect(screen.getByText("needs both declared and measured values")).toBeInTheDocument();
    expect(screen.getByText("Effective (none)")).toBeInTheDocument();
  });

  it("marks declared and measured that agree as consistent", async () => {
    routes({
      "GET /products/SKU-1": json(product({ physicalProfile: { ...MEASURED_PROFILE, discrepancy: false } })),
    });
    mount();
    expect(await screen.findByText("Consistent")).toHaveAttribute("data-tone", "success");
  });

  it("explains a 404 product-not-found with the problem's title and detail", async () => {
    mockApi({ "GET /products/NOPE": problem(404, "product-not-found", "Product not found", "product not found") });
    mount("NOPE");
    expect(await screen.findByText("No product is registered under this SKU.")).toBeInTheDocument();
    expect(screen.getByText("Product not found")).toBeInTheDocument();
    expect(screen.getByText("product not found")).toBeInTheDocument();
  });
});

describe("classify form", () => {
  const route = "PUT /products/SKU-1/classification";

  it("is prefilled from the current classification and sends the API's body", async () => {
    let classified = false;
    const api = routes({
      "GET /products/SKU-1": () =>
        json(classified ? product({ version: 5, classification: { handlingTags: ["Fragile"], classificationSource: "native" } }) : product()),
      [route]: () => {
        classified = true;
        return json({ sku: "SKU-1", handlingTags: ["Fragile"], classificationSource: "native", version: 5 }, 200);
      },
    });
    const user = userEvent.setup();
    mount();
    await screen.findByRole("heading", { name: "SKU-1" });
    const f = form("Classify product");
    expect(f.getByLabelText("Hazmat")).toBeChecked();
    expect(f.getByLabelText("TemperatureSensitive")).toBeChecked();
    expect(f.getByLabelText(/Temperature class/)).toHaveValue("Frozen");
    expect(f.getByLabelText("DOT hazard class")).toHaveValue("3");

    await user.click(f.getByLabelText("Hazmat"));
    await user.click(f.getByLabelText("TemperatureSensitive"));
    await user.click(f.getByLabelText("Fragile"));
    await user.click(f.getByRole("button", { name: "Save classification" }));

    expect(await screen.findByText("Classification saved (version 5).")).toBeInTheDocument();
    // hidden fields are not sent: no temperature class / DOT class without their tags
    expect(api.to(route)[0].body).toEqual({ handlingTags: ["Fragile"] });
    // the product is re-read and the page shows the new version, the confirmation stays
    await waitFor(() => expect(screen.getByText(/· version/)).toHaveTextContent("version 5"));
    expect(screen.getByText("Classification saved (version 5).")).toBeInTheDocument();
  });

  it("sends tags in the stable order with the temperature and DOT classes, and says when it was the first", async () => {
    const api = routes({
      "GET /products/SKU-2": json(BARE),
      "PUT /products/SKU-2/classification": json(
        { sku: "SKU-2", handlingTags: ["Hazmat", "TemperatureSensitive"], classificationSource: "native", version: 2 },
        201,
      ),
    });
    const user = userEvent.setup();
    mount("SKU-2");
    await screen.findByText("Not classified yet.");
    const f = form("Classify product");
    await user.click(f.getByLabelText("TemperatureSensitive"));
    await user.click(f.getByLabelText("Hazmat"));
    await user.selectOptions(f.getByLabelText(/Temperature class/), "Chilled");
    await user.type(f.getByLabelText("DOT hazard class"), "9");
    await user.click(f.getByRole("button", { name: "Save classification" }));

    expect(await screen.findByText("Classification set (version 2).")).toBeInTheDocument();
    expect(api.to("PUT /products/SKU-2/classification")[0].body).toEqual({
      handlingTags: ["Hazmat", "TemperatureSensitive"],
      temperatureClass: "Chilled",
      dotHazardClass: 9,
    });
  });

  it.each([
    ["no tag", ["Hazmat", "TemperatureSensitive"], "", "Choose at least one handling tag."],
    ["DOT out of range", [], "12", "The DOT hazard class is a whole number from 1 to 9, or left empty."],
  ])("validates before sending (%s)", async (_name, untick, dot, message) => {
    const api = routes();
    const user = userEvent.setup();
    mount();
    await screen.findByRole("heading", { name: "SKU-1" });
    const f = form("Classify product");
    for (const tag of untick) await user.click(f.getByLabelText(tag));
    if (dot) {
      await user.clear(f.getByLabelText("DOT hazard class"));
      await user.type(f.getByLabelText("DOT hazard class"), dot);
    }
    await user.click(f.getByRole("button", { name: "Save classification" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(api.to(route)).toHaveLength(0);
  });

  it("requires a temperature class for a TemperatureSensitive product", async () => {
    const api = mockApi({ "GET /products/SKU-2": json(BARE) });
    const user = userEvent.setup();
    mount("SKU-2");
    await screen.findByText("Not classified yet.");
    const f = form("Classify product");
    await user.click(f.getByLabelText("TemperatureSensitive"));
    await user.click(f.getByRole("button", { name: "Save classification" }));
    expect(await screen.findByText("A TemperatureSensitive product needs a temperature class.")).toBeInTheDocument();
    expect(api.calls).toHaveLength(1); // only the GET
  });

  it("surfaces a rejected classification with title and detail", async () => {
    routes({
      [route]: problem(
        400,
        "dot-hazard-class-not-applicable",
        "DOT hazard class is only meaningful when the hazmat tag is present",
        "dot hazard class is only meaningful when the hazmat tag is present",
      ),
    });
    const user = userEvent.setup();
    mount();
    await screen.findByRole("heading", { name: "SKU-1" });
    await user.click(form("Classify product").getByRole("button", { name: "Save classification" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("DOT hazard class is only meaningful when the hazmat tag is present");
    expect(alert).toHaveTextContent("dot hazard class is only meaningful when the hazmat tag is present");
    expect(alert).toHaveTextContent("HTTP 400");
  });
});

describe("declare dimensions form", () => {
  const route = "PUT /products/SKU-1/dimensions/declared";

  it("is prefilled from the declared values and sends whole numbers", async () => {
    const api = routes({ [route]: json({ ...MEASURED_PROFILE, version: 7 }) });
    const user = userEvent.setup();
    mount();
    await screen.findByRole("heading", { name: "SKU-1" });
    const f = form("Declare dimensions");
    expect(f.getByLabelText(/Declared length/)).toHaveValue("200");
    await user.clear(f.getByLabelText(/Declared weight/));
    await user.type(f.getByLabelText(/Declared weight/), "1600");
    await user.click(f.getByRole("button", { name: "Declare dimensions" }));

    expect(await screen.findByText("Declared dimensions saved (version 7).")).toBeInTheDocument();
    expect(api.to(route)[0].body).toEqual({ lengthMm: 200, widthMm: 120, heightMm: 80, weightG: 1600 });
    await waitFor(() => expect(api.to("GET /products/SKU-1")).toHaveLength(2));
  });

  it.each([
    ["length 0", /Declared length/, "0", "Length must be a whole number of millimetres from 1 to 20,000."],
    ["fractional height", /Declared height/, "8.5", "Height must be a whole number of millimetres from 1 to 20,000."],
    ["heavy", /Declared weight/, "2000001", "Weight must be a whole number of grams from 1 to 2,000,000."],
  ])("validates the API's bounds before sending (%s)", async (_n, field, value, message) => {
    const api = routes();
    const user = userEvent.setup();
    mount();
    await screen.findByRole("heading", { name: "SKU-1" });
    const f = form("Declare dimensions");
    await user.clear(f.getByLabelText(field));
    await user.type(f.getByLabelText(field), value);
    await user.click(f.getByRole("button", { name: "Declare dimensions" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(api.to(route)).toHaveLength(0);
  });

  it("surfaces a server rejection with title and detail", async () => {
    routes({ [route]: problem(400, "invalid-dimension", "Dimension out of range", "each dimension must be 1..20000 mm") });
    const user = userEvent.setup();
    mount();
    await screen.findByRole("heading", { name: "SKU-1" });
    await user.click(form("Declare dimensions").getByRole("button", { name: "Declare dimensions" }));
    expect(await screen.findByText("Dimension out of range")).toBeInTheDocument();
    expect(screen.getByText("each dimension must be 1..20000 mm")).toBeInTheDocument();
  });
});

describe("record measurement form", () => {
  const route = "PUT /products/SKU-1/dimensions/measured";

  async function fill(user: ReturnType<typeof userEvent.setup>, device = "") {
    await screen.findByRole("heading", { name: "SKU-1" });
    const f = form("Record measurement");
    await user.type(f.getByLabelText(/Measured length/), "205");
    await user.type(f.getByLabelText(/Measured width/), "121");
    await user.type(f.getByLabelText(/Measured height/), "82");
    await user.type(f.getByLabelText(/Measured weight/), "1720");
    fireEvent.change(f.getByLabelText(/Measured at \(UTC\)/), { target: { value: "2026-10-06T14:05" } });
    if (device) await user.type(f.getByLabelText("Device id"), device);
    return f;
  }

  it("sends the reading with the time read as UTC and reports a discrepancy", async () => {
    const api = routes({ [route]: json({ ...MEASURED_PROFILE, version: 8 }) });
    const user = userEvent.setup();
    mount();
    const f = await fill(user, "CUBISCAN-03");
    await user.click(f.getByRole("button", { name: "Record measurement" }));

    expect(
      await screen.findByText("Measurement recorded (version 8). It differs from the declared values by more than 10 %."),
    ).toBeInTheDocument();
    expect(api.to(route)[0].body).toEqual({
      lengthMm: 205,
      widthMm: 121,
      heightMm: 82,
      weightG: 1720,
      measuredAt: "2026-10-06T14:05:00Z",
      deviceId: "CUBISCAN-03",
    });
  });

  it("omits the device id for a manual measurement", async () => {
    const api = routes({ [route]: json({ ...MEASURED_PROFILE, discrepancy: false, version: 8 }) });
    const user = userEvent.setup();
    mount();
    const f = await fill(user);
    await user.click(f.getByRole("button", { name: "Record measurement" }));
    expect(await screen.findByText("Measurement recorded (version 8).")).toBeInTheDocument();
    expect(api.to(route)[0].body).not.toHaveProperty("deviceId");
  });

  it("requires the measured time", async () => {
    const api = routes();
    const user = userEvent.setup();
    mount();
    const f = await fill(user);
    fireEvent.change(f.getByLabelText(/Measured at \(UTC\)/), { target: { value: "" } });
    await user.click(f.getByRole("button", { name: "Record measurement" }));
    expect(await screen.findByText("Pick when the unit was measured (UTC).")).toBeInTheDocument();
    expect(api.to(route)).toHaveLength(0);
  });

  it("explains 409 stale-measurement and still shows the problem's title and detail", async () => {
    routes({
      [route]: problem(409, "stale-measurement", "A newer measurement is already recorded", "measurement is older than the current one"),
    });
    const user = userEvent.setup();
    mount();
    const f = await fill(user);
    await user.click(f.getByRole("button", { name: "Record measurement" }));
    expect(await screen.findByText(/A newer measurement is already recorded for this product, so this one was not applied/)).toBeInTheDocument();
    expect(screen.getByText("A newer measurement is already recorded")).toBeInTheDocument();
    expect(screen.getByText("measurement is older than the current one")).toBeInTheDocument();
  });

  it("shows a measured-at-in-future rejection without the stale guidance", async () => {
    routes({
      [route]: problem(400, "measured-at-in-future", "Measurement time is in the future", "measuredAt is after the current time"),
    });
    const user = userEvent.setup();
    mount();
    const f = await fill(user);
    await user.click(f.getByRole("button", { name: "Record measurement" }));
    expect(await screen.findByText("Measurement time is in the future")).toBeInTheDocument();
    expect(screen.queryByText(/so this one was not applied/)).not.toBeInTheDocument();
  });
});
