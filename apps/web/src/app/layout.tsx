import type { Metadata } from "next";
import { Inter } from "next/font/google";
import { Rail } from "@/components/Rail";
import { SyncButton } from "@/components/SyncButton";
import "./globals.css";

// Inter, as in CCI (NRDS). next/font serves it from this app: no third-party request.
const inter = Inter({ variable: "--font-inter", subsets: ["latin"] });

export const metadata: Metadata = {
  title: "Dzinga",
  description: "Your work + personal OS for the job search.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={inter.variable} data-color-scheme="light">
      <body>
        <div className="shell">
          <Rail />
          <main className="main">
            <header className="topbar"><SyncButton /></header>
            {children}
          </main>
        </div>
      </body>
    </html>
  );
}
