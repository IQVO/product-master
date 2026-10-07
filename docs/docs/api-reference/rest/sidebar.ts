import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/product-master-api",
    },
    {
      type: "category",
      label: "Products",
      link: {
        type: "doc",
        id: "api-reference/rest/products",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-products",
          label: "List products",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/register-product",
          label: "Register a product or change its description",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-product",
          label: "Get a product",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Classification",
      link: {
        type: "doc",
        id: "api-reference/rest/classification",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/classify-product",
          label: "Set or replace a product's handling classification",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-product-classification",
          label: "Get a product's handling classification",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Physical profile",
      link: {
        type: "doc",
        id: "api-reference/rest/physical-profile",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/declare-dimensions",
          label: "Declare a product's unit dimensions and weight",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "api-reference/rest/record-measurement",
          label: "Record a measurement of one unit",
          className: "api-method put",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-physical-profile",
          label: "Get a product's physical profile",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/healthz",
          label: "Liveness probe",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/readyz",
          label: "Readiness probe",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Reports",
      link: {
        type: "doc",
        id: "api-reference/rest/reports",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-master-data-quality",
          label: "Master data quality per day and current coverage",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-reports-freshness",
          label: "How far the analytics projection is behind",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
