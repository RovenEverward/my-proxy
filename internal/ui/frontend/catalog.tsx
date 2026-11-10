import { createRoot } from "react-dom/client";
import { CatalogPage } from "./pages/catalog";
import "./styles/index.css";
import "./styles/pages/catalog.css";

createRoot(document.getElementById("root")!).render(<CatalogPage />);
