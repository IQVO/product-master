import { describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { RegisterProductScreen } from "./RegisterProductScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { BARE } from "../test/fixtures";

function mount() {
  return render(
    <MemoryRouter initialEntries={["/register"]}>
      <RegisterProductScreen />
    </MemoryRouter>,
  );
}

describe("RegisterProductScreen", () => {
  it("registers a new SKU (201) and links to it", async () => {
    const api = mockApi({ "PUT /products/SKU-9": json({ ...BARE, sku: "SKU-9", description: "Box" }, 201) });
    const user = userEvent.setup();
    mount();
    await user.type(screen.getByLabelText(/^SKU/), "SKU-9");
    await user.type(screen.getByLabelText("Description"), "Box");
    await user.click(screen.getByRole("button", { name: "Register" }));

    const ok = await screen.findByRole("status");
    expect(ok).toHaveTextContent("Registered SKU-9 (version 1).");
    expect(within(ok).getByRole("link", { name: "Open SKU-9" })).toHaveAttribute("href", "/products/SKU-9");
    expect(api.to("PUT /products/SKU-9")[0].body).toEqual({ description: "Box" });
  });

  it("says so when the SKU already existed (200) and only the description changed", async () => {
    mockApi({ "PUT /products/SKU-9": json({ ...BARE, sku: "SKU-9", version: 3 }, 200) });
    const user = userEvent.setup();
    mount();
    await user.type(screen.getByLabelText(/^SKU/), "SKU-9");
    await user.click(screen.getByRole("button", { name: "Register" }));
    expect(await screen.findByText(/SKU-9 was already registered; its description is now the one entered \(version 3\)/)).toBeInTheDocument();
  });

  it("surfaces the service's problem title and detail", async () => {
    mockApi({
      "PUT /products/SKU-9": problem(
        409,
        "concurrent-modification",
        "The resource was modified by another request; re-fetch the latest version and retry",
        "concurrent modification",
      ),
    });
    const user = userEvent.setup();
    mount();
    await user.type(screen.getByLabelText(/^SKU/), "SKU-9");
    await user.click(screen.getByRole("button", { name: "Register" }));
    expect(await screen.findByText(/modified by another request/)).toBeInTheDocument();
    expect(screen.getByText("concurrent modification")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it.each([
    ["", "Enter a SKU."],
    ["SKU 1", "A SKU cannot contain spaces, control characters or '/'."],
  ])("validates the SKU before sending (%j)", async (sku, message) => {
    const api = mockApi({});
    const user = userEvent.setup();
    mount();
    if (sku) await user.type(screen.getByLabelText(/^SKU/), sku);
    await user.click(screen.getByRole("button", { name: "Register" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
    await waitFor(() => expect(api.calls).toHaveLength(0));
  });

  it("refuses a description over 200 characters", async () => {
    const api = mockApi({});
    const user = userEvent.setup();
    mount();
    await user.type(screen.getByLabelText(/^SKU/), "SKU-9");
    await user.click(screen.getByLabelText("Description"));
    await user.paste("x".repeat(201));
    await user.click(screen.getByRole("button", { name: "Register" }));
    expect(await screen.findByText("A description is at most 200 characters.")).toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
  });
});
